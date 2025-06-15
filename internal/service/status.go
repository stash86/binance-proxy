package service

import (
	"sync"
	"time"
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
		statusTracker = &StatusTracker{
			startTime: time.Now(),
			isHealthy: true,
		}
	})
	return statusTracker
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
		errorRate = float64(st.errors) / float64(st.requests) * 100
	}

	status := Status{
		Service:   "binance-proxy",
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
	st.mu.Lock()
	defer st.mu.Unlock()
	st.errors++
	st.lastError = err
	st.lastErrorAt = time.Now()

	// Consider service unhealthy if error rate is too high
	if st.requests > 100 && float64(st.errors)/float64(st.requests) > 0.1 {
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
