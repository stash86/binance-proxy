package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	once          sync.Once
	globalMetrics *Metrics
)

// Metrics holds all application metrics
type Metrics struct {
	// Connection metrics (using atomic for lock-free access)
	activeWebSocketConnections int64
	totalWebSocketConnections  int64
	webSocketReconnections     int64
	webSocketMessages          int64
	webSocketErrors            int64

	// Request metrics (using atomic)
	totalRequests   int64
	cachedRequests  int64
	proxiedRequests int64
	failedRequests  int64

	// Response time metrics
	responseTimeSum   int64 // Sum in nanoseconds
	responseTimeCount int64 // Count for average
	maxResponseTime   int64 // Nanoseconds
	minResponseTime   int64 // Nanoseconds

	// Rate limiting metrics (atomic)
	rateLimitHits  int64
	rateLimitWaits int64

	// Start time
	startTime int64 // Unix timestamp
}

// MetricsSnapshot represents metrics at a point in time
type MetricsSnapshot struct {
	Uptime                     time.Duration
	ActiveWebSocketConnections int64
	TotalWebSocketConnections  int64
	WebSocketReconnections     int64
	WebSocketMessages          int64
	WebSocketErrors            int64

	TotalRequests   int64
	CachedRequests  int64
	ProxiedRequests int64
	FailedRequests  int64

	MaxResponseTime time.Duration
	MinResponseTime time.Duration
	AvgResponseTime time.Duration

	RateLimitHits  int64
	RateLimitWaits int64

	MemoryUsage MemoryUsage
}

// MemoryUsage represents memory usage metrics
type MemoryUsage struct {
	Alloc      uint64
	TotalAlloc uint64
	Sys        uint64
	NumGC      uint32
	Goroutines int
}

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	once.Do(func() {
		globalMetrics = &Metrics{
			minResponseTime: int64(^uint64(0) >> 1), // Max int64 initially
			startTime:       time.Now().Unix(),
		}
	})
	return globalMetrics
}

// IncrementWebSocketConnection increments active WebSocket connections
func (m *Metrics) IncrementWebSocketConnection() {
	atomic.AddInt64(&m.activeWebSocketConnections, 1)
	atomic.AddInt64(&m.totalWebSocketConnections, 1)
}

// DecrementWebSocketConnection decrements active WebSocket connections
func (m *Metrics) DecrementWebSocketConnection() {
	atomic.AddInt64(&m.activeWebSocketConnections, -1)
}

// IncrementWebSocketReconnection increments WebSocket reconnections
func (m *Metrics) IncrementWebSocketReconnection() {
	atomic.AddInt64(&m.webSocketReconnections, 1)
}

// IncrementWebSocketMessage increments WebSocket message count
func (m *Metrics) IncrementWebSocketMessage() {
	atomic.AddInt64(&m.webSocketMessages, 1)
}

// IncrementWebSocketError increments WebSocket error count
func (m *Metrics) IncrementWebSocketError() {
	atomic.AddInt64(&m.webSocketErrors, 1)
}

// RecordRequest records a request with its type and duration
func (m *Metrics) RecordRequest(endpoint string, cached bool, duration time.Duration) {
	atomic.AddInt64(&m.totalRequests, 1)

	if cached {
		atomic.AddInt64(&m.cachedRequests, 1)
	} else {
		atomic.AddInt64(&m.proxiedRequests, 1)
	}

	// Update response times
	durationNs := duration.Nanoseconds()
	atomic.AddInt64(&m.responseTimeSum, durationNs)
	atomic.AddInt64(&m.responseTimeCount, 1)

	// Update min/max
	for {
		current := atomic.LoadInt64(&m.maxResponseTime)
		if durationNs <= current || atomic.CompareAndSwapInt64(&m.maxResponseTime, current, durationNs) {
			break
		}
	}
	for {
		current := atomic.LoadInt64(&m.minResponseTime)
		if durationNs >= current || atomic.CompareAndSwapInt64(&m.minResponseTime, current, durationNs) {
			break
		}
	}
}

// IncrementFailedRequest increments failed request count
func (m *Metrics) IncrementFailedRequest() {
	atomic.AddInt64(&m.failedRequests, 1)
}

// IncrementRateLimitHit increments rate limit hit count
func (m *Metrics) IncrementRateLimitHit() {
	atomic.AddInt64(&m.rateLimitHits, 1)
}

// IncrementRateLimitWait increments rate limit wait count
func (m *Metrics) IncrementRateLimitWait() {
	atomic.AddInt64(&m.rateLimitWaits, 1)
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	startTime := time.Unix(atomic.LoadInt64(&m.startTime), 0)
	uptime := time.Since(startTime)

	// Calculate average response time
	sum := atomic.LoadInt64(&m.responseTimeSum)
	count := atomic.LoadInt64(&m.responseTimeCount)
	var avgResponseTime time.Duration
	if count > 0 {
		avgResponseTime = time.Duration(sum / count)
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return MetricsSnapshot{
		Uptime:                     uptime,
		ActiveWebSocketConnections: atomic.LoadInt64(&m.activeWebSocketConnections),
		TotalWebSocketConnections:  atomic.LoadInt64(&m.totalWebSocketConnections),
		WebSocketReconnections:     atomic.LoadInt64(&m.webSocketReconnections),
		WebSocketMessages:          atomic.LoadInt64(&m.webSocketMessages),
		WebSocketErrors:            atomic.LoadInt64(&m.webSocketErrors),

		TotalRequests:   atomic.LoadInt64(&m.totalRequests),
		CachedRequests:  atomic.LoadInt64(&m.cachedRequests),
		ProxiedRequests: atomic.LoadInt64(&m.proxiedRequests),
		FailedRequests:  atomic.LoadInt64(&m.failedRequests),

		MaxResponseTime: time.Duration(atomic.LoadInt64(&m.maxResponseTime)),
		MinResponseTime: time.Duration(atomic.LoadInt64(&m.minResponseTime)),
		AvgResponseTime: avgResponseTime,

		RateLimitHits:  atomic.LoadInt64(&m.rateLimitHits),
		RateLimitWaits: atomic.LoadInt64(&m.rateLimitWaits),

		MemoryUsage: MemoryUsage{
			Alloc:      memStats.Alloc,
			TotalAlloc: memStats.TotalAlloc,
			Sys:        memStats.Sys,
			NumGC:      memStats.NumGC,
			Goroutines: runtime.NumGoroutine(),
		},
	}
}

// HTTPHandler provides an HTTP endpoint for metrics
func (m *Metrics) HTTPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	snapshot := m.GetSnapshot()

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "# Binance Proxy Metrics\n")
	fmt.Fprintf(w, "uptime_seconds %d\n", int64(snapshot.Uptime.Seconds()))
	fmt.Fprintf(w, "active_websocket_connections %d\n", snapshot.ActiveWebSocketConnections)
	fmt.Fprintf(w, "total_websocket_connections %d\n", snapshot.TotalWebSocketConnections)
	fmt.Fprintf(w, "websocket_reconnections %d\n", snapshot.WebSocketReconnections)
	fmt.Fprintf(w, "websocket_messages %d\n", snapshot.WebSocketMessages)
	fmt.Fprintf(w, "websocket_errors %d\n", snapshot.WebSocketErrors)

	fmt.Fprintf(w, "total_requests %d\n", snapshot.TotalRequests)
	fmt.Fprintf(w, "cached_requests %d\n", snapshot.CachedRequests)
	fmt.Fprintf(w, "proxied_requests %d\n", snapshot.ProxiedRequests)
	fmt.Fprintf(w, "failed_requests %d\n", snapshot.FailedRequests)

	fmt.Fprintf(w, "max_response_time_nanoseconds %d\n", snapshot.MaxResponseTime.Nanoseconds())
	fmt.Fprintf(w, "min_response_time_nanoseconds %d\n", snapshot.MinResponseTime.Nanoseconds())
	fmt.Fprintf(w, "avg_response_time_nanoseconds %d\n", snapshot.AvgResponseTime.Nanoseconds())

	fmt.Fprintf(w, "rate_limit_hits %d\n", snapshot.RateLimitHits)
	fmt.Fprintf(w, "rate_limit_waits %d\n", snapshot.RateLimitWaits)

	// Memory metrics
	fmt.Fprintf(w, "memory_alloc_bytes %d\n", snapshot.MemoryUsage.Alloc)
	fmt.Fprintf(w, "memory_total_alloc_bytes %d\n", snapshot.MemoryUsage.TotalAlloc)
	fmt.Fprintf(w, "memory_sys_bytes %d\n", snapshot.MemoryUsage.Sys)
	fmt.Fprintf(w, "memory_num_gc %d\n", snapshot.MemoryUsage.NumGC)
	fmt.Fprintf(w, "goroutines %d\n", snapshot.MemoryUsage.Goroutines)
}
