package service

import (
	"sync"
	"time"
)

const (
	statusServiceName          = "binance-proxy"
	statusUnhealthyMinRequests = 100
	statusUnhealthyErrorRate   = 0.10
)

// StatusTracker tracks the overall status of the proxy service
type StatusTracker struct {
	mu          sync.RWMutex
	startTime   time.Time
	isHealthy   bool
	lastError   error
	lastErrorAt time.Time
	requests    int64
	errors      int64
}

var (
	statusTracker     *StatusTracker
	statusTrackerOnce sync.Once
)

// GetStatusTracker returns the global status tracker instance
func GetStatusTracker() *StatusTracker {
	statusTrackerOnce.Do(func() {
		statusTracker = newStatusTracker(time.Now())
	})
	return statusTracker
}

func newStatusTracker(startTime time.Time) *StatusTracker {
	return &StatusTracker{
		startTime: startTime,
		isHealthy: true,
	}
}

// Status represents the current status of the proxy
type Status struct {
	Service     string    `json:"service"`
	Healthy     bool      `json:"healthy"`
	StartTime   time.Time `json:"start_time"`
	Uptime      string    `json:"uptime"`
	Requests    int64     `json:"requests"`
	Errors      int64     `json:"errors"`
	ErrorRate   float64   `json:"error_rate"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt string    `json:"last_error_at,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// GetStatus returns the current status
func (st *StatusTracker) GetStatus() Status {
	st.mu.RLock()
	defer st.mu.RUnlock()

	uptime := time.Since(st.startTime)
	errorRate := float64(0)
	if st.requests > 0 {
		errorRate = st.errorRateLocked() * 100
	}

	status := Status{
		Service:   statusServiceName,
		Healthy:   st.isHealthy,
		StartTime: st.startTime,
		Uptime:    uptime.String(),
		Requests:  st.requests,
		Errors:    st.errors,
		ErrorRate: errorRate,
		Timestamp: time.Now(),
	}

	if st.lastError != nil {
		status.LastError = st.lastError.Error()
		status.LastErrorAt = st.lastErrorAt.Format(time.RFC3339)
	}

	return status
}

// RecordRequest increments the request counter
func (st *StatusTracker) RecordRequest() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.requests++
}

// RecordError increments the error counter and records the error
func (st *StatusTracker) RecordError(err error) {
	if err == nil {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.errors++
	st.lastError = err
	st.lastErrorAt = time.Now()

	// Consider service unhealthy if error rate is too high
	if st.requests > statusUnhealthyMinRequests && st.errorRateLocked() > statusUnhealthyErrorRate {
		st.isHealthy = false
	}
}

// SetHealthy manually sets the health status
func (st *StatusTracker) SetHealthy(healthy bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.isHealthy = healthy
}

// Reset resets all counters (useful for testing)
func (st *StatusTracker) Reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.startTime = time.Now()
	st.isHealthy = true
	st.lastError = nil
	st.lastErrorAt = time.Time{}
	st.requests = 0
	st.errors = 0
}

func (st *StatusTracker) errorRateLocked() float64 {
	if st.requests == 0 {
		return 0
	}
	return float64(st.errors) / float64(st.requests)
}
