package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"binance-proxy/internal/logcache"

	log "github.com/sirupsen/logrus"
)

const (
	defaultSpotWeightLimit    = 1200
	defaultFuturesWeightLimit = 2400
	weightLimitThreshold      = 0.9
)

var banExpiryRegexp = regexp.MustCompile(`\b(\d{10,13})\b`)

type BanDetector struct {
	mu sync.RWMutex

	// Ban status per class
	spotBanned    bool
	futuresBanned bool

	// Recovery times
	spotRecoveryTime    time.Time
	futuresRecoveryTime time.Time

	// Error counters for gradual ban detection
	spotErrorCount    int
	futuresErrorCount int
	lastSpotError     time.Time
	lastFuturesError  time.Time

	// API Weight tracking
	spotWeightUsed     int
	futuresWeightUsed  int
	spotWeightLimit    int
	futuresWeightLimit int
	spotWeightReset    time.Time
	futuresWeightReset time.Time

	// Exponential backoff tracking
	spotBackoffCount    int
	futuresBackoffCount int
}

var globalBanDetector = &BanDetector{}

func GetBanDetector() *BanDetector {
	return globalBanDetector
}

func (bd *BanDetector) IsBanned(class Class) bool {
	bd.mu.Lock()
	defer bd.mu.Unlock()

	return bd.isBannedLocked(class, time.Now())
}

func (bd *BanDetector) isBannedLocked(class Class, now time.Time) bool {
	if class == SPOT {
		if !bd.spotBanned {
			return false
		}
		if now.Before(bd.spotRecoveryTime) {
			return true
		}
		bd.spotBanned = false
		log.Infof("%s API ban lifted, resuming normal operation", class)
		return false
	}

	if !bd.futuresBanned {
		return false
	}
	if now.Before(bd.futuresRecoveryTime) {
		return true
	}
	bd.futuresBanned = false
	log.Infof("%s API ban lifted, resuming normal operation", class)
	return false
}

func (bd *BanDetector) CheckResponse(class Class, resp *http.Response, err error) bool {
	now := time.Now()

	statusCode := 0
	retryAfterUntil := time.Time{}
	bodyBanUntil := time.Time{}
	if resp != nil {
		statusCode = resp.StatusCode
		retryAfterUntil = bd.parseRetryAfter(resp, now)
		if statusCode == http.StatusTeapot && retryAfterUntil.IsZero() {
			bodyBanUntil = bd.parseBanExpiryNonDestructive(resp)
		}
	}

	bd.mu.Lock()
	defer bd.mu.Unlock()

	// Check API weight headers if response is available
	if resp != nil {
		bd.updateWeightInfo(class, resp, now)

		// Check for explicit ban status codes before local weight throttling.
		switch statusCode {
		case http.StatusTeapot: // Binance uses 418 for IP bans.
			banUntil := retryAfterUntil
			if banUntil.IsZero() {
				banUntil = bodyBanUntil
			}
			if banUntil.IsZero() {
				// If both methods fail, use 10 minutes default
				banUntil = now.Add(10 * time.Minute)
				log.Errorf("%s API IP banned (418), no expiry found, suspending requests for 10 minutes until %v", class, banUntil)
			} else {
				log.Errorf("%s API IP banned (418), suspending requests until %v", class, banUntil)
			}
			bd.setBanned(class, banUntil)
			bd.resetBackoffCount(class) // Reset backoff on explicit ban
			return true
		case http.StatusTooManyRequests:
			banUntil := retryAfterUntil
			if banUntil.IsZero() {
				// Fallback to 1 minute default
				banUntil = now.Add(1 * time.Minute)
				log.Warnf("%s API rate limited (429), no Retry-After header, suspending requests for 1 minute until %v", class, banUntil)
			} else {
				log.Warnf("%s API rate limited (429), suspending requests until %v", class, banUntil)
			}
			bd.setBanned(class, banUntil)
			bd.resetBackoffCount(class) // Reset backoff on explicit rate limit
			return true
		case http.StatusForbidden:
			bd.setBanned(class, now.Add(5*time.Minute))
			log.Warnf("%s API access forbidden (403), suspending requests until %v", class, bd.getRecoveryTime(class))
			return true
		}

		// Check if approaching weight limits
		if bd.isApproachingWeightLimit(class) {
			waitTime := bd.getWeightResetDuration(class, now)
			if waitTime > 0 {
				bd.setBanned(class, now.Add(waitTime))
				logcache.LogOncePerDuration("warn", fmt.Sprintf("%s API weight limit approaching, suspending requests until %v", class, bd.getRecoveryTime(class)))
				return true
			}
		}
	}

	// Check for connection errors that might indicate bans
	if isLikelyBanConnectionError(err) {
		bd.incrementErrorCount(class, now)

		// If too many errors in short time, use exponential backoff
		errorCount := bd.getErrorCount(class)
		if errorCount >= 5 {
			backoffDuration := bd.getExponentialBackoff(class)
			bd.setBanned(class, now.Add(backoffDuration))
			bd.resetErrorCount(class)
			log.Warnf("%s API connection issues detected (%d errors), suspending requests for %v until %v", class, errorCount, backoffDuration, bd.getRecoveryTime(class))
			return true
		}
	}

	// Reset error count and backoff on successful request
	if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		bd.resetErrorCount(class)
		bd.resetBackoffCount(class)
	}

	return false
}

func (bd *BanDetector) parseBanExpiryNonDestructive(resp *http.Response) time.Time {
	if resp == nil || resp.Body == nil {
		return time.Time{}
	}

	// Read response body without consuming it
	originalBody := resp.Body
	body, err := io.ReadAll(resp.Body)
	_ = originalBody.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return time.Time{}
	}

	// Parse JSON response for banned until timestamp. Fall back to the raw body
	// so minor upstream response shape changes do not hide the ban expiry.
	msg := string(body)
	var banResponse struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &banResponse); err == nil && banResponse.Msg != "" {
		msg = banResponse.Msg
	}

	// Look for unix timestamp in message (10 or 13 digits)
	matches := banExpiryRegexp.FindStringSubmatch(msg)
	if len(matches) > 1 {
		if timestamp, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
			// Convert milliseconds to seconds if needed
			if timestamp > 9999999999 {
				timestamp = timestamp / 1000
			}
			return time.Unix(timestamp, 0)
		}
	}

	return time.Time{}
}

func (bd *BanDetector) parseRetryAfter(resp *http.Response, now time.Time) time.Time {
	if resp == nil {
		return time.Time{}
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return time.Time{}
	}
	retryAfter = strings.TrimSpace(retryAfter)

	// Parse seconds to wait
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		if seconds < 0 {
			return time.Time{}
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}

	if retryAt, err := http.ParseTime(retryAfter); err == nil {
		if retryAt.Before(now) {
			return now
		}
		return retryAt
	}

	return time.Time{}
}

func (bd *BanDetector) updateWeightInfo(class Class, resp *http.Response, now time.Time) {
	headerWeight, hasHeaderWeight := parseWeightHeader(resp)
	nextReset := now.Truncate(time.Minute).Add(time.Minute)

	// Spot API headers
	if class == SPOT {
		// Set default limit if not set
		if bd.spotWeightLimit == 0 {
			bd.spotWeightLimit = defaultSpotWeightLimit
		}
		if bd.spotWeightReset.IsZero() || !now.Before(bd.spotWeightReset) {
			bd.spotWeightReset = nextReset
			if !hasHeaderWeight {
				bd.spotWeightUsed = 0
			}
		}
		if hasHeaderWeight {
			bd.spotWeightUsed = headerWeight
			return
		}
		// Fallback: estimate weight usage (most kline requests are weight 1)
		bd.spotWeightUsed += 1
		return
	}

	// Futures API headers
	if bd.futuresWeightLimit == 0 {
		bd.futuresWeightLimit = defaultFuturesWeightLimit
	}
	if bd.futuresWeightReset.IsZero() || !now.Before(bd.futuresWeightReset) {
		bd.futuresWeightReset = nextReset
		if !hasHeaderWeight {
			bd.futuresWeightUsed = 0
		}
	}
	if hasHeaderWeight {
		bd.futuresWeightUsed = headerWeight
		return
	}
	// Fallback: estimate weight usage
	bd.futuresWeightUsed += 1
}

func parseWeightHeader(resp *http.Response) (int, bool) {
	if resp == nil {
		return 0, false
	}
	used := resp.Header.Get("X-MBX-USED-WEIGHT-1M")
	if used == "" {
		return 0, false
	}
	weight, err := strconv.Atoi(used)
	if err != nil {
		return 0, false
	}
	return weight, true
}

func (bd *BanDetector) getExponentialBackoff(class Class) time.Duration {
	var backoffCount int
	if class == SPOT {
		bd.spotBackoffCount++
		backoffCount = bd.spotBackoffCount
	} else {
		bd.futuresBackoffCount++
		backoffCount = bd.futuresBackoffCount
	}

	// Exponential backoff: 2^n seconds, max 10 minutes
	duration := time.Duration(1<<uint(backoffCount)) * time.Second
	maxDuration := 10 * time.Minute
	if duration > maxDuration {
		duration = maxDuration
	}

	return duration
}

func (bd *BanDetector) resetBackoffCount(class Class) {
	if class == SPOT {
		bd.spotBackoffCount = 0
	} else {
		bd.futuresBackoffCount = 0
	}
}

func (bd *BanDetector) setBanned(class Class, recoveryTime time.Time) {
	if class == SPOT {
		bd.spotBanned = true
		bd.spotRecoveryTime = recoveryTime
	} else {
		bd.futuresBanned = true
		bd.futuresRecoveryTime = recoveryTime
	}
}

func (bd *BanDetector) getRecoveryTime(class Class) time.Time {
	if class == SPOT {
		return bd.spotRecoveryTime
	}
	return bd.futuresRecoveryTime
}

func (bd *BanDetector) incrementErrorCount(class Class, now time.Time) {
	if class == SPOT {
		// Reset counter if last error was more than 1 minute ago
		if now.Sub(bd.lastSpotError) > time.Minute {
			bd.spotErrorCount = 0
		}
		bd.spotErrorCount++
		bd.lastSpotError = now
	} else {
		if now.Sub(bd.lastFuturesError) > time.Minute {
			bd.futuresErrorCount = 0
		}
		bd.futuresErrorCount++
		bd.lastFuturesError = now
	}
}

func (bd *BanDetector) getErrorCount(class Class) int {
	if class == SPOT {
		return bd.spotErrorCount
	}
	return bd.futuresErrorCount
}

func (bd *BanDetector) resetErrorCount(class Class) {
	if class == SPOT {
		bd.spotErrorCount = 0
	} else {
		bd.futuresErrorCount = 0
	}
}

func (bd *BanDetector) GetBanStatus(class Class) (bool, time.Time) {
	bd.mu.Lock()
	defer bd.mu.Unlock()

	banned := bd.isBannedLocked(class, time.Now())
	if class == SPOT {
		return banned, bd.spotRecoveryTime
	}
	return banned, bd.futuresRecoveryTime
}

func (bd *BanDetector) isApproachingWeightLimit(class Class) bool {
	if class == SPOT {
		if bd.spotWeightLimit > 0 {
			usage := float64(bd.spotWeightUsed) / float64(bd.spotWeightLimit)
			return usage > weightLimitThreshold
		}
	} else {
		if bd.futuresWeightLimit > 0 {
			usage := float64(bd.futuresWeightUsed) / float64(bd.futuresWeightLimit)
			return usage > weightLimitThreshold
		}
	}
	return false
}

func (bd *BanDetector) getWeightResetDuration(class Class, now time.Time) time.Duration {
	resetTime := bd.spotWeightReset
	if class != SPOT {
		resetTime = bd.futuresWeightReset
	}
	if resetTime.IsZero() || !now.Before(resetTime) {
		resetTime = now.Truncate(time.Minute).Add(time.Minute)
	}
	return resetTime.Sub(now)
}

func (bd *BanDetector) GetWeightInfo(class Class) (used int, limit int, resetTime time.Time) {
	bd.mu.RLock()
	defer bd.mu.RUnlock()

	if class == SPOT {
		return bd.spotWeightUsed, bd.spotWeightLimit, bd.spotWeightReset
	}
	return bd.futuresWeightUsed, bd.futuresWeightLimit, bd.futuresWeightReset
}

func isLikelyBanConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errorMsg := strings.ToLower(err.Error())
	return strings.Contains(errorMsg, "connection refused") ||
		strings.Contains(errorMsg, "connection reset by peer") ||
		strings.Contains(errorMsg, "deadline exceeded") ||
		strings.Contains(errorMsg, "no route to host") ||
		strings.Contains(errorMsg, "timeout")
}
