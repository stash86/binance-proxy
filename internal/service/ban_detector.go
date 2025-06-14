package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

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

	now := time.Now()

	if class == SPOT {
		if bd.spotBanned && now.Before(bd.spotRecoveryTime) {
			return true
		} else if bd.spotBanned && now.After(bd.spotRecoveryTime) {
			// Recovery time passed, clear ban
			bd.spotBanned = false
			log.Infof("%s API ban lifted, resuming normal operation", class)
		}
	} else {
		if bd.futuresBanned && now.Before(bd.futuresRecoveryTime) {
			return true
		} else if bd.futuresBanned && now.After(bd.futuresRecoveryTime) {
			// Recovery time passed, clear ban
			bd.futuresBanned = false
			log.Infof("%s API ban lifted, resuming normal operation", class)
		}
	}

	return false
}

func (bd *BanDetector) CheckResponse(class Class, resp *http.Response, err error) bool {
	bd.mu.Lock()
	defer bd.mu.Unlock()

	now := time.Now()

	// Check API weight headers if response is available
	if resp != nil {
		bd.updateWeightInfo(class, resp)

		// Check if approaching weight limits
		if bd.isApproachingWeightLimit(class) {
			waitTime := bd.getWeightResetTime(class)
			if waitTime > 0 {
				bd.setBanned(class, now.Add(waitTime))
				log.Warnf("%s API weight limit approaching, suspending requests until %v", class, bd.getRecoveryTime(class))
				return true
			}
		}

		// Check for explicit ban status codes
		switch resp.StatusCode {
		case 418: // IP banned
			banUntil := bd.parseRetryAfter(resp, now)
			if banUntil.IsZero() {
				// Fallback to parsing response body for timestamp
				banUntil = bd.parseBanExpiryNonDestructive(resp)
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
		case 429: // Rate limit exceeded
			banUntil := bd.parseRetryAfter(resp, now)
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
		case 403: // Forbidden
			bd.setBanned(class, now.Add(5*time.Minute))
			log.Warnf("%s API access forbidden (403), suspending requests until %v", class, bd.getRecoveryTime(class))
			return true
		}
	}

	// Check for connection errors that might indicate bans
	if err != nil {
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "connection refused") ||
			strings.Contains(errorMsg, "timeout") ||
			strings.Contains(errorMsg, "no route to host") {

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
	}

	// Reset error count and backoff on successful request
	if resp != nil && resp.StatusCode == 200 {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return time.Time{}
	}

	// Restore the body for later use
	resp.Body = io.NopCloser(bytes.NewBuffer(body))

	// Parse JSON response for banned until timestamp
	var banResponse struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}

	if err := json.Unmarshal(body, &banResponse); err == nil {
		// Look for unix timestamp in message (10 or 13 digits)
		re := regexp.MustCompile(`(\d{10,13})`)
		matches := re.FindStringSubmatch(banResponse.Msg)
		if len(matches) > 1 {
			if timestamp, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
				// Convert milliseconds to seconds if needed
				if timestamp > 9999999999 {
					timestamp = timestamp / 1000
				}
				return time.Unix(timestamp, 0)
			}
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

	// Parse seconds to wait
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return now.Add(time.Duration(seconds) * time.Second)
	}

	return time.Time{}
}

func (bd *BanDetector) updateWeightInfo(class Class, resp *http.Response) {
	// Spot API headers
	if class == SPOT {
		if used := resp.Header.Get("X-MBX-USED-WEIGHT-1M"); used != "" {
			if weight, err := strconv.Atoi(used); err == nil {
				bd.spotWeightUsed = weight
			}
		} else {
			// Fallback: estimate weight usage (most kline requests are weight 1)
			bd.spotWeightUsed += 1
		}

		// Set default limit if not set
		if bd.spotWeightLimit == 0 {
			bd.spotWeightLimit = 1200 // Default spot weight limit per minute
		}
	} else {
		// Futures API headers
		if used := resp.Header.Get("X-MBX-USED-WEIGHT-1M"); used != "" {
			if weight, err := strconv.Atoi(used); err == nil {
				bd.futuresWeightUsed = weight
			}
		} else {
			// Fallback: estimate weight usage
			bd.futuresWeightUsed += 1
		}

		// Set default limit if not set
		if bd.futuresWeightLimit == 0 {
			bd.futuresWeightLimit = 2400 // Default futures weight limit per minute
		}
	}

	// Reset weight counters every minute
	now := time.Now()
	if class == SPOT && now.After(bd.spotWeightReset) {
		bd.spotWeightUsed = 0
		bd.spotWeightReset = now.Truncate(time.Minute).Add(time.Minute)
	} else if class != SPOT && now.After(bd.futuresWeightReset) {
		bd.futuresWeightUsed = 0
		bd.futuresWeightReset = now.Truncate(time.Minute).Add(time.Minute)
	}
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
	bd.mu.RLock()
	defer bd.mu.RUnlock()

	if class == SPOT {
		return bd.spotBanned, bd.spotRecoveryTime
	}
	return bd.futuresBanned, bd.futuresRecoveryTime
}

func (bd *BanDetector) isApproachingWeightLimit(class Class) bool {
	if class == SPOT {
		if bd.spotWeightLimit > 0 {
			usage := float64(bd.spotWeightUsed) / float64(bd.spotWeightLimit)
			return usage > 0.9 // 90% threshold
		}
	} else {
		if bd.futuresWeightLimit > 0 {
			usage := float64(bd.futuresWeightUsed) / float64(bd.futuresWeightLimit)
			return usage > 0.9 // 90% threshold
		}
	}
	return false
}

func (bd *BanDetector) getWeightResetTime(class Class) time.Duration {
	// Weight limits reset every minute, so wait until next minute
	now := time.Now()
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	return nextMinute.Sub(now)
}

func (bd *BanDetector) GetWeightInfo(class Class) (used int, limit int, resetTime time.Time) {
	bd.mu.RLock()
	defer bd.mu.RUnlock()

	if class == SPOT {
		return bd.spotWeightUsed, bd.spotWeightLimit, bd.spotWeightReset
	}
	return bd.futuresWeightUsed, bd.futuresWeightLimit, bd.futuresWeightReset
}
