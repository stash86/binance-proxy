package service

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"binance-proxy/internal/metrics"

	"golang.org/x/time/rate"
)

var (
	limitersMu     sync.RWMutex
	spotLimiter    *rate.Limiter
	futuresLimiter *rate.Limiter
)

// InitializeRateLimiters initializes the rate limiters with custom settings
func InitializeRateLimiters(spotRPS float64, spotBurst int, futuresRPS float64, futuresBurst int) {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	
	spotLimiter = rate.NewLimiter(rate.Limit(spotRPS), spotBurst)
	futuresLimiter = rate.NewLimiter(rate.Limit(futuresRPS), futuresBurst)
}

func init() {
	// Default initialization
	InitializeRateLimiters(20, 1200, 40, 2400)
}

// GetRateLimiter returns the appropriate rate limiter for the given class
func GetRateLimiter(class Class) *rate.Limiter {
	limitersMu.RLock()
	defer limitersMu.RUnlock()
	
	if class == SPOT {
		return spotLimiter
	}
	return futuresLimiter
}

// RateWait waits according to rate limiting rules with improved weight calculation
func RateWait(ctx context.Context, class Class, method, path string, query url.Values) {
	weight := calculateWeight(path, method, query)
	
	limiter := GetRateLimiter(class)
	
	// Record rate limiting metrics
	m := metrics.GetMetrics()
	
	// Check if we would be rate limited
	if !limiter.Allow() {
		m.RecordRateLimitHit()
	}
	
	// Wait for rate limiter
	if err := limiter.WaitN(ctx, weight); err != nil {
		// Context was cancelled
		return
	}
	
	if weight > 1 {
		m.RecordRateLimitWait()
	}
}

// calculateWeight calculates the request weight based on endpoint and parameters
func calculateWeight(path, method string, query url.Values) int {
	weight := 1
	
	switch path {
	case "/fapi/v1/klines":
		weight = calculateKlineWeight(query)
	case "/api/v3/klines":
		weight = calculateKlineWeight(query)
	case "/api/v3/depth":
		weight = calculateDepthWeightSpot(query)
	case "/fapi/v1/depth":
		weight = calculateDepthWeightFutures(query)
	case "/api/v3/ticker/24hr", "/fapi/v1/ticker/24hr":
		if query.Get("symbol") == "" {
			weight = 40 // All symbols
		} else {
			weight = 1 // Single symbol
		}
	case "/api/v3/exchangeInfo", "/fapi/v1/exchangeInfo":
		weight = 10
	case "/api/v3/account":
		weight = 10
	case "/api/v3/myTrades":
		weight = 10
	case "/api/v3/order":
		if method == http.MethodGet {
			weight = 2
		} else {
			weight = 1
		}
	case "/fapi/v1/userTrades":
		weight = 5
	case "/fapi/v2/account":
		weight = 5
	case "/api/v3/allOrders":
		weight = 10
	case "/fapi/v1/allOrders":
		weight = 5
	case "/api/v3/openOrders":
		if query.Get("symbol") == "" {
			weight = 40 // All symbols
		} else {
			weight = 3 // Single symbol
		}
	case "/fapi/v1/openOrders":
		if query.Get("symbol") == "" {
			weight = 5 // All symbols
		} else {
			weight = 1 // Single symbol
		}
	}
	
	return weight
}

// calculateKlineWeight calculates weight for kline requests based on limit
func calculateKlineWeight(query url.Values) int {
	limitStr := query.Get("limit")
	if limitStr == "" {
		return 1
	}
	
	limitInt, err := strconv.Atoi(limitStr)
	if err != nil {
		return 1
	}
	
	switch {
	case limitInt <= 100:
		return 1
	case limitInt <= 500:
		return 2
	case limitInt <= 1000:
		return 5
	case limitInt <= 1500:
		return 10
	default:
		return 10
	}
}

// calculateDepthWeightSpot calculates weight for spot depth requests
func calculateDepthWeightSpot(query url.Values) int {
	limitStr := query.Get("limit")
	if limitStr == "" {
		return 1
	}
	
	limitInt, err := strconv.Atoi(limitStr)
	if err != nil {
		return 1
	}
	
	switch {
	case limitInt <= 100:
		return 1
	case limitInt <= 500:
		return 5
	case limitInt == 1000:
		return 10
	case limitInt == 5000:
		return 50
	default:
		return 1
	}
}

// calculateDepthWeightFutures calculates weight for futures depth requests
func calculateDepthWeightFutures(query url.Values) int {
	limitStr := query.Get("limit")
	if limitStr == "" {
		return 2
	}
	
	limitInt, err := strconv.Atoi(limitStr)
	if err != nil {
		return 2
	}
	
	switch {
	case limitInt <= 50:
		return 2
	case limitInt == 100:
		return 5
	case limitInt == 500:
		return 10
	case limitInt == 1000:
		return 20
	default:
		return 2
	}
}
