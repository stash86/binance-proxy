package throttle

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	log "github.com/sirupsen/logrus"
)

// AdaptiveThrottler provides advanced request throttling with adaptive rate limiting
type AdaptiveThrottler struct {
	limiters        map[string]*adaptiveLimiter
	mu              sync.RWMutex
	baseRate        rate.Limit
	baseBurst       int
	maxRate         rate.Limit
	minRate         rate.Limit
	successWindow   time.Duration
	errorWindow     time.Duration
	cleanupInterval time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	ticker          *time.Ticker
}

// adaptiveLimiter tracks success/error rates and adjusts limits dynamically
type adaptiveLimiter struct {
	limiter       *rate.Limiter
	successes     int64
	errors        int64
	lastSuccess   time.Time
	lastError     time.Time
	lastAdjust    time.Time
	currentRate   rate.Limit
	consecutiveErrors int64
	consecutiveSuccesses int64
}

// Config holds throttling configuration
type Config struct {
	BaseRPS         float64       `long:"base-rps" env:"BASE_RPS" description:"Base requests per second" default:"10"`
	BaseBurst       int           `long:"base-burst" env:"BASE_BURST" description:"Base burst capacity" default:"20"`
	MaxRPS          float64       `long:"max-rps" env:"MAX_RPS" description:"Maximum requests per second" default:"100"`
	MinRPS          float64       `long:"min-rps" env:"MIN_RPS" description:"Minimum requests per second" default:"1"`
	SuccessWindow   time.Duration `long:"success-window" env:"SUCCESS_WINDOW" description:"Success tracking window" default:"1m"`
	ErrorWindow     time.Duration `long:"error-window" env:"ERROR_WINDOW" description:"Error tracking window" default:"5m"`
	CleanupInterval time.Duration `long:"cleanup-interval" env:"CLEANUP_INTERVAL" description:"Cleanup interval for idle limiters" default:"10m"`
	AdaptiveEnabled bool          `long:"adaptive-enabled" env:"ADAPTIVE_ENABLED" description:"Enable adaptive rate limiting" default:"true"`
}

// NewAdaptiveThrottler creates a new adaptive throttler
func NewAdaptiveThrottler(ctx context.Context, config *Config) *AdaptiveThrottler {
	throttlerCtx, cancel := context.WithCancel(ctx)
	
	at := &AdaptiveThrottler{
		limiters:        make(map[string]*adaptiveLimiter),
		baseRate:        rate.Limit(config.BaseRPS),
		baseBurst:       config.BaseBurst,
		maxRate:         rate.Limit(config.MaxRPS),
		minRate:         rate.Limit(config.MinRPS),
		successWindow:   config.SuccessWindow,
		errorWindow:     config.ErrorWindow,
		cleanupInterval: config.CleanupInterval,
		ctx:             throttlerCtx,
		cancel:          cancel,
	}
	
	// Start cleanup routine
	at.ticker = time.NewTicker(config.CleanupInterval)
	go at.cleanupLoop()
	
	log.Infof("Adaptive throttler initialized - Base: %.1f RPS, Range: %.1f-%.1f RPS", 
		config.BaseRPS, config.MinRPS, config.MaxRPS)
	
	return at
}

// Stop stops the throttler
func (at *AdaptiveThrottler) Stop() {
	if at.cancel != nil {
		at.cancel()
	}
	if at.ticker != nil {
		at.ticker.Stop()
	}
}

// Allow checks if a request should be allowed for the given key
func (at *AdaptiveThrottler) Allow(key string) bool {
	at.mu.Lock()
	limiter, exists := at.limiters[key]
	if !exists {
		limiter = &adaptiveLimiter{
			limiter:     rate.NewLimiter(at.baseRate, at.baseBurst),
			currentRate: at.baseRate,
			lastAdjust:  time.Now(),
		}
		at.limiters[key] = limiter
	}
	at.mu.Unlock()
	
	return limiter.limiter.Allow()
}

// RecordSuccess records a successful request and may adjust the rate limit
func (at *AdaptiveThrottler) RecordSuccess(key string) {
	at.mu.Lock()
	limiter, exists := at.limiters[key]
	if !exists {
		at.mu.Unlock()
		return
	}
	
	limiter.successes++
	limiter.lastSuccess = time.Now()
	limiter.consecutiveSuccesses++
	limiter.consecutiveErrors = 0
	
	// Adaptive adjustment - increase rate on consecutive successes
	if limiter.consecutiveSuccesses >= 10 && 
		time.Since(limiter.lastAdjust) > at.successWindow &&
		limiter.currentRate < at.maxRate {
		
		newRate := rate.Limit(float64(limiter.currentRate) * 1.1)
		if newRate > at.maxRate {
			newRate = at.maxRate
		}
		
		limiter.limiter.SetLimit(newRate)
		limiter.currentRate = newRate
		limiter.lastAdjust = time.Now()
		limiter.consecutiveSuccesses = 0
		
		log.Debugf("Throttle: increased rate limit for %s to %.1f RPS", key, float64(newRate))
	}
	at.mu.Unlock()
}

// RecordError records a failed request and may adjust the rate limit
func (at *AdaptiveThrottler) RecordError(key string) {
	at.mu.Lock()
	limiter, exists := at.limiters[key]
	if !exists {
		at.mu.Unlock()
		return
	}
	
	limiter.errors++
	limiter.lastError = time.Now()
	limiter.consecutiveErrors++
	limiter.consecutiveSuccesses = 0
	
	// Adaptive adjustment - decrease rate on consecutive errors
	if limiter.consecutiveErrors >= 3 && 
		time.Since(limiter.lastAdjust) > time.Minute &&
		limiter.currentRate > at.minRate {
		
		newRate := rate.Limit(float64(limiter.currentRate) * 0.8)
		if newRate < at.minRate {
			newRate = at.minRate
		}
		
		limiter.limiter.SetLimit(newRate)
		limiter.currentRate = newRate
		limiter.lastAdjust = time.Now()
		limiter.consecutiveErrors = 0
		
		log.Debugf("Throttle: decreased rate limit for %s to %.1f RPS", key, float64(newRate))
	}
	at.mu.Unlock()
}

// GetLimiter returns the rate limiter for a specific key
func (at *AdaptiveThrottler) GetLimiter(key string) *rate.Limiter {
	at.mu.RLock()
	defer at.mu.RUnlock()
	
	if limiter, exists := at.limiters[key]; exists {
		return limiter.limiter
	}
	return nil
}

// cleanupLoop removes inactive limiters
func (at *AdaptiveThrottler) cleanupLoop() {
	defer at.ticker.Stop()
	
	for {
		select {
		case <-at.ctx.Done():
			return
		case <-at.ticker.C:
			at.cleanup()
		}
	}
}

// cleanup removes inactive limiters to prevent memory leaks
func (at *AdaptiveThrottler) cleanup() {
	at.mu.Lock()
	defer at.mu.Unlock()
	
	now := time.Now()
	removed := 0
	
	for key, limiter := range at.limiters {
		// Remove if inactive for longer than cleanup interval
		if now.Sub(limiter.lastSuccess) > at.cleanupInterval &&
			now.Sub(limiter.lastError) > at.cleanupInterval {
			delete(at.limiters, key)
			removed++
		}
	}
	
	if removed > 0 {
		log.Debugf("Throttle: cleaned up %d inactive limiters", removed)
	}
}

// GetStats returns throttling statistics
func (at *AdaptiveThrottler) GetStats() map[string]interface{} {
	at.mu.RLock()
	defer at.mu.RUnlock()
	
	totalSuccesses := int64(0)
	totalErrors := int64(0)
	activeLimiters := len(at.limiters)
	rateDistribution := make(map[string]int)
	
	for _, limiter := range at.limiters {
		totalSuccesses += limiter.successes
		totalErrors += limiter.errors
		
		// Categorize rate limits
		rate := float64(limiter.currentRate)
		switch {
		case rate <= 1:
			rateDistribution["very_low"]++
		case rate <= 10:
			rateDistribution["low"]++
		case rate <= 50:
			rateDistribution["medium"]++
		case rate <= 100:
			rateDistribution["high"]++
		default:
			rateDistribution["very_high"]++
		}
	}
	
	errorRate := float64(0)
	if totalSuccesses+totalErrors > 0 {
		errorRate = float64(totalErrors) / float64(totalSuccesses+totalErrors) * 100
	}
	
	return map[string]interface{}{
		"active_limiters":     activeLimiters,
		"total_successes":     totalSuccesses,
		"total_errors":        totalErrors,
		"error_rate_percent":  errorRate,
		"base_rate_rps":       float64(at.baseRate),
		"rate_distribution":   rateDistribution,
		"success_window":      at.successWindow.String(),
		"error_window":        at.errorWindow.String(),
	}
}
