package metrics

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// Metrics holds all application metrics with memory optimization
type Metrics struct {
	mu sync.RWMutex
		// Connection metrics (using atomic for lock-free access)
	activeWebSocketConnections int64
	totalWebSocketConnections  int64
	webSocketReconnections     int64
	webSocketMessages          int64
	webSocketErrors            int64
	webSocketPingLatency       int64 // in microseconds
	webSocketCircuitBreakerTrips int64
	
	// Request metrics (using atomic)
	totalRequests       int64
	cachedRequests      int64
	proxiedRequests     int64
	failedRequests      int64
	
	// Response time metrics (circular buffer for memory efficiency)
	responseTimeBuffer  []time.Duration
	responseTimeIndex   int64
	responseTimeSum     int64 // Sum in nanoseconds for avg calculation
	maxResponseTime     int64 // Nanoseconds
	minResponseTime     int64 // Nanoseconds
	
	// Rate limiting metrics (atomic)
	rateLimitHits      int64
	rateLimitWaits     int64
	
	// Error metrics (using sync.Map for concurrent access, limited size)
	errorCounts        sync.Map // map[string]*int64
	maxErrorTypes      int      // Limit number of error types to prevent memory leak
	
	// Start time
	startTime          int64 // Unix timestamp for memory efficiency
	
	// Per-endpoint metrics (limited map size)
	endpointMetrics    sync.Map // map[string]*EndpointMetrics
	maxEndpoints       int      // Limit number of endpoints
	
	// Memory management
	bufferSize         int
	cleanupInterval    time.Duration
	lastCleanup        int64
}

// EndpointMetrics with memory-efficient atomic counters
type EndpointMetrics struct {
	requestCount    int64        // atomic
	cachedCount     int64        // atomic  
	proxiedCount    int64        // atomic
	errorCount      int64        // atomic
	totalDuration   int64        // atomic, nanoseconds
	maxDuration     int64        // atomic, nanoseconds
	minDuration     int64        // atomic, nanoseconds
	lastAccess      int64        // atomic, unix timestamp
}

// GetMetrics returns the global metrics instance with memory optimization
func GetMetrics() *Metrics {
	once.Do(func() {
		bufferSize := 1000 // Circular buffer size - configurable
		globalMetrics = &Metrics{
			responseTimeBuffer:  make([]time.Duration, bufferSize),
			responseTimeIndex:   0,
			minResponseTime:     int64(^uint64(0) >> 1), // Max int64 initially
			startTime:          time.Now().Unix(),
			bufferSize:         bufferSize,
			maxErrorTypes:      50,  // Limit error types to prevent memory leak
			maxEndpoints:       100, // Limit endpoints to prevent memory leak
			cleanupInterval:    5 * time.Minute,
			lastCleanup:        time.Now().Unix(),
		}
		
		// Start background cleanup routine
		go globalMetrics.backgroundCleanup()
	})
	return globalMetrics
}

// backgroundCleanup performs periodic memory cleanup
func (m *Metrics) backgroundCleanup() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		m.performCleanup()
	}
}

// performCleanup removes old/unused metrics to prevent memory leaks
func (m *Metrics) performCleanup() {
	now := time.Now().Unix()
	
	// Cleanup old endpoint metrics (not accessed in last hour)
	cutoff := now - 3600 // 1 hour
	endpointCount := 0
	
	m.endpointMetrics.Range(func(key, value interface{}) bool {
		endpointCount++
		if em, ok := value.(*EndpointMetrics); ok {
			lastAccess := atomic.LoadInt64(&em.lastAccess)
			if lastAccess < cutoff {
				m.endpointMetrics.Delete(key)
				log.WithField("endpoint", key).Debug("Cleaned up old endpoint metrics")
			}
		}
		return true
	})
	
	// If we have too many endpoints, remove the oldest ones
	if endpointCount > m.maxEndpoints {
		m.cleanupOldestEndpoints(endpointCount - m.maxEndpoints)
	}
	
	// Cleanup old error types
	errorCount := 0
	m.errorCounts.Range(func(key, value interface{}) bool {
		errorCount++
		return true
	})
	
	if errorCount > m.maxErrorTypes {
		m.cleanupOldestErrors(errorCount - m.maxErrorTypes)
	}
	
	// Force garbage collection if memory usage is high
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	if memStats.Alloc > 50*1024*1024 { // 50MB threshold
		runtime.GC()
		log.WithField("memory_mb", memStats.Alloc/1024/1024).Debug("Forced garbage collection due to high memory usage")
	}
	
	atomic.StoreInt64(&m.lastCleanup, now)
}

// cleanupOldestEndpoints removes the oldest endpoint metrics
func (m *Metrics) cleanupOldestEndpoints(toRemove int) {
	type endpointAge struct {
		endpoint   string
		lastAccess int64
	}
	
	var endpoints []endpointAge
	
	m.endpointMetrics.Range(func(key, value interface{}) bool {
		if em, ok := value.(*EndpointMetrics); ok {
			endpoints = append(endpoints, endpointAge{
				endpoint:   key.(string),
				lastAccess: atomic.LoadInt64(&em.lastAccess),
			})
		}
		return true
	})
	
	// Sort by last access time (oldest first)
	for i := 0; i < len(endpoints)-1; i++ {
		for j := i + 1; j < len(endpoints); j++ {
			if endpoints[i].lastAccess > endpoints[j].lastAccess {
				endpoints[i], endpoints[j] = endpoints[j], endpoints[i]
			}
		}
	}
	
	// Remove oldest endpoints
	for i := 0; i < toRemove && i < len(endpoints); i++ {
		m.endpointMetrics.Delete(endpoints[i].endpoint)
		log.WithField("endpoint", endpoints[i].endpoint).Debug("Removed old endpoint metric to free memory")
	}
}

// cleanupOldestErrors removes error count entries (basic cleanup)
func (m *Metrics) cleanupOldestErrors(toRemove int) {
	removed := 0
	m.errorCounts.Range(func(key, value interface{}) bool {
		if removed < toRemove {
			m.errorCounts.Delete(key)
			removed++
			log.WithField("error_type", key).Debug("Removed old error count to free memory")
		}
		return removed < toRemove
	})
}

// IncrementWebSocketConnection increments active WebSocket connections
func (m *Metrics) IncrementWebSocketConnection() {
	atomic.AddInt64(&m.ActiveWebSocketConnections, 1)
	atomic.AddInt64(&m.TotalWebSocketConnections, 1)
}

// DecrementWebSocketConnection decrements active WebSocket connections
func (m *Metrics) DecrementWebSocketConnection() {
	atomic.AddInt64(&m.ActiveWebSocketConnections, -1)
}

// IncrementWebSocketReconnection increments WebSocket reconnections
func (m *Metrics) IncrementWebSocketReconnection() {
	atomic.AddInt64(&m.WebSocketReconnections, 1)
}

// IncrementWebSocketMessage increments WebSocket message count
func (m *Metrics) IncrementWebSocketMessage() {
	atomic.AddInt64(&m.webSocketMessages, 1)
}

// IncrementWebSocketError increments WebSocket error count
func (m *Metrics) IncrementWebSocketError() {
	atomic.AddInt64(&m.webSocketErrors, 1)
}

// RecordWebSocketPingLatency records WebSocket ping latency in microseconds
func (m *Metrics) RecordWebSocketPingLatency(latency time.Duration) {
	atomic.StoreInt64(&m.webSocketPingLatency, latency.Microseconds())
}

// IncrementWebSocketCircuitBreakerTrip increments circuit breaker trips
func (m *Metrics) IncrementWebSocketCircuitBreakerTrip() {
	atomic.AddInt64(&m.webSocketCircuitBreakerTrips, 1)
}

// RecordRequest records a request with its type and duration
func (m *Metrics) RecordRequest(endpoint string, cached bool, duration time.Duration) {
	atomic.AddInt64(&m.TotalRequests, 1)
	
	if cached {
		atomic.AddInt64(&m.CachedRequests, 1)
	} else {
		atomic.AddInt64(&m.ProxiedRequests, 1)
	}
	
	// Update response times
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.ResponseTimes = append(m.ResponseTimes, duration)
	if len(m.ResponseTimes) > 1000 {
		// Keep only last 1000 measurements
		m.ResponseTimes = m.ResponseTimes[1:]
	}
	
	if duration > m.MaxResponseTime {
		m.MaxResponseTime = duration
	}
	if duration < m.MinResponseTime {
		m.MinResponseTime = duration
	}
	
	// Calculate average
	var total time.Duration
	for _, d := range m.ResponseTimes {
		total += d
	}
	m.AvgResponseTime = total / time.Duration(len(m.ResponseTimes))
	
	// Update endpoint metrics
	if m.EndpointMetrics[endpoint] == nil {
		m.EndpointMetrics[endpoint] = &EndpointMetrics{
			MinDuration: time.Duration(^uint64(0) >> 1),
		}
	}
	
	ep := m.EndpointMetrics[endpoint]
	ep.RequestCount++
	if cached {
		ep.CachedCount++
	} else {
		ep.ProxiedCount++
	}
	ep.TotalDuration += duration
	if duration > ep.MaxDuration {
		ep.MaxDuration = duration
	}
	if duration < ep.MinDuration {
		ep.MinDuration = duration
	}
}

// RecordError records an error
func (m *Metrics) RecordError(errorType string) {
	atomic.AddInt64(&m.FailedRequests, 1)
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.ErrorCounts[errorType]++
}

// RecordRateLimitHit records a rate limit hit
func (m *Metrics) RecordRateLimitHit() {
	atomic.AddInt64(&m.RateLimitHits, 1)
}

// RecordRateLimitWait records a rate limit wait
func (m *Metrics) RecordRateLimitWait() {
	atomic.AddInt64(&m.RateLimitWaits, 1)
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
		uptime := time.Since(m.StartTime)
	
	snapshot := MetricsSnapshot{
		Uptime:                     uptime,
		ActiveWebSocketConnections: atomic.LoadInt64(&m.ActiveWebSocketConnections),
		TotalWebSocketConnections:  atomic.LoadInt64(&m.TotalWebSocketConnections),
		WebSocketReconnections:     atomic.LoadInt64(&m.WebSocketReconnections),
		WebSocketMessages:          atomic.LoadInt64(&m.webSocketMessages),
		WebSocketErrors:            atomic.LoadInt64(&m.webSocketErrors),
		WebSocketPingLatency:       atomic.LoadInt64(&m.webSocketPingLatency),
		WebSocketCircuitBreakerTrips: atomic.LoadInt64(&m.webSocketCircuitBreakerTrips),
		TotalRequests:              atomic.LoadInt64(&m.TotalRequests),
		CachedRequests:             atomic.LoadInt64(&m.CachedRequests),
		ProxiedRequests:            atomic.LoadInt64(&m.ProxiedRequests),
		FailedRequests:             atomic.LoadInt64(&m.FailedRequests),
		MaxResponseTime:            m.MaxResponseTime,
		MinResponseTime:            m.MinResponseTime,
		AvgResponseTime:            m.AvgResponseTime,
		RateLimitHits:              atomic.LoadInt64(&m.RateLimitHits),
		RateLimitWaits:             atomic.LoadInt64(&m.RateLimitWaits),
		ErrorCounts:                make(map[string]int64),
		EndpointMetrics:            make(map[string]EndpointMetricsSnapshot),
	}
	
	// Copy error counts
	for k, v := range m.ErrorCounts {
		snapshot.ErrorCounts[k] = v
	}
	
	// Copy endpoint metrics
	for k, v := range m.EndpointMetrics {
		avgDuration := time.Duration(0)
		if v.RequestCount > 0 {
			avgDuration = v.TotalDuration / time.Duration(v.RequestCount)
		}
		
		snapshot.EndpointMetrics[k] = EndpointMetricsSnapshot{
			RequestCount: v.RequestCount,
			CachedCount:  v.CachedCount,
			ProxiedCount: v.ProxiedCount,
			ErrorCount:   v.ErrorCount,
			AvgDuration:  avgDuration,
			MaxDuration:  v.MaxDuration,
			MinDuration:  v.MinDuration,
		}
	}
	
	return snapshot
}

type MetricsSnapshot struct {
	Uptime                       time.Duration                        `json:"uptime"`
	ActiveWebSocketConnections   int64                                `json:"active_websocket_connections"`
	TotalWebSocketConnections    int64                                `json:"total_websocket_connections"`
	WebSocketReconnections       int64                                `json:"websocket_reconnections"`
	WebSocketMessages            int64                                `json:"websocket_messages"`
	WebSocketErrors              int64                                `json:"websocket_errors"`
	WebSocketPingLatency         int64                                `json:"websocket_ping_latency_us"`
	WebSocketCircuitBreakerTrips int64                                `json:"websocket_circuit_breaker_trips"`
	TotalRequests                int64                                `json:"total_requests"`
	CachedRequests               int64                                `json:"cached_requests"`
	ProxiedRequests              int64                                `json:"proxied_requests"`
	FailedRequests               int64                                `json:"failed_requests"`
	MaxResponseTime              time.Duration                        `json:"max_response_time"`
	MinResponseTime              time.Duration                        `json:"min_response_time"`
	AvgResponseTime              time.Duration                        `json:"avg_response_time"`
	RateLimitHits                int64                                `json:"rate_limit_hits"`
	RateLimitWaits               int64                                `json:"rate_limit_waits"`
	ErrorCounts                  map[string]int64                     `json:"error_counts"`
	EndpointMetrics              map[string]EndpointMetricsSnapshot   `json:"endpoint_metrics"`
}

type EndpointMetricsSnapshot struct {
	RequestCount int64         `json:"request_count"`
	CachedCount  int64         `json:"cached_count"`
	ProxiedCount int64         `json:"proxied_count"`
	ErrorCount   int64         `json:"error_count"`
	AvgDuration  time.Duration `json:"avg_duration"`
	MaxDuration  time.Duration `json:"max_duration"`
	MinDuration  time.Duration `json:"min_duration"`
}

// StartMetricsServer starts an HTTP server for metrics with memory monitoring
func StartMetricsServer(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	
	// Metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics := GetMetrics()
		snapshot := metrics.GetSnapshot()
		
		// Simple text format with memory metrics
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "# Binance Proxy Metrics\n")
		fmt.Fprintf(w, "uptime_seconds %d\n", int64(snapshot.Uptime.Seconds()))		fmt.Fprintf(w, "active_websocket_connections %d\n", snapshot.ActiveWebSocketConnections)
		fmt.Fprintf(w, "total_websocket_connections %d\n", snapshot.TotalWebSocketConnections)
		fmt.Fprintf(w, "websocket_reconnections %d\n", snapshot.WebSocketReconnections)
		fmt.Fprintf(w, "websocket_messages %d\n", snapshot.WebSocketMessages)
		fmt.Fprintf(w, "websocket_errors %d\n", snapshot.WebSocketErrors)
		fmt.Fprintf(w, "websocket_ping_latency_microseconds %d\n", snapshot.WebSocketPingLatency)
		fmt.Fprintf(w, "websocket_circuit_breaker_trips %d\n", snapshot.WebSocketCircuitBreakerTrips)
		fmt.Fprintf(w, "total_requests %d\n", snapshot.TotalRequests)
		fmt.Fprintf(w, "cached_requests %d\n", snapshot.CachedRequests)
		fmt.Fprintf(w, "proxied_requests %d\n", snapshot.ProxiedRequests)
		fmt.Fprintf(w, "failed_requests %d\n", snapshot.FailedRequests)
		fmt.Fprintf(w, "max_response_time_ms %d\n", snapshot.MaxResponseTime.Milliseconds())
		fmt.Fprintf(w, "min_response_time_ms %d\n", snapshot.MinResponseTime.Milliseconds())
		fmt.Fprintf(w, "avg_response_time_ms %d\n", snapshot.AvgResponseTime.Milliseconds())
		fmt.Fprintf(w, "rate_limit_hits %d\n", snapshot.RateLimitHits)
		fmt.Fprintf(w, "rate_limit_waits %d\n", snapshot.RateLimitWaits)
		
		// Memory metrics
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		fmt.Fprintf(w, "memory_alloc_bytes %d\n", memStats.Alloc)
		fmt.Fprintf(w, "memory_total_alloc_bytes %d\n", memStats.TotalAlloc)
		fmt.Fprintf(w, "memory_sys_bytes %d\n", memStats.Sys)
		fmt.Fprintf(w, "memory_heap_objects %d\n", memStats.HeapObjects)
		fmt.Fprintf(w, "memory_gc_runs %d\n", memStats.NumGC)
		fmt.Fprintf(w, "memory_gc_cpu_percent %.2f\n", memStats.GCCPUFraction*100)
		fmt.Fprintf(w, "memory_next_gc_bytes %d\n", memStats.NextGC)
		fmt.Fprintf(w, "memory_stack_bytes %d\n", memStats.StackSys)
		
		// Error counts
		for errorType, count := range snapshot.ErrorCounts {
			fmt.Fprintf(w, "error_count{type=\"%s\"} %d\n", errorType, count)
		}
		
		// Endpoint metrics
		for endpoint, metrics := range snapshot.EndpointMetrics {
			fmt.Fprintf(w, "endpoint_requests{endpoint=\"%s\"} %d\n", endpoint, metrics.RequestCount)
			fmt.Fprintf(w, "endpoint_cached{endpoint=\"%s\"} %d\n", endpoint, metrics.CachedCount)
			fmt.Fprintf(w, "endpoint_proxied{endpoint=\"%s\"} %d\n", endpoint, metrics.ProxiedCount)
			fmt.Fprintf(w, "endpoint_errors{endpoint=\"%s\"} %d\n", endpoint, metrics.ErrorCount)
			fmt.Fprintf(w, "endpoint_avg_duration_ms{endpoint=\"%s\"} %d\n", endpoint, metrics.AvgDuration.Milliseconds())
		}
	})
	
	// Memory-specific endpoint
	mux.HandleFunc("/memory", func(w http.ResponseWriter, r *http.Request) {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		
		// Force GC if requested
		if r.URL.Query().Get("gc") == "true" {
			runtime.GC()
			runtime.ReadMemStats(&memStats)
		}
		
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"alloc_mb": %.2f,
			"total_alloc_mb": %.2f,
			"sys_mb": %.2f,
			"heap_objects": %d,
			"gc_runs": %d,
			"gc_cpu_percent": %.4f,
			"next_gc_mb": %.2f,
			"stack_mb": %.2f,
			"goroutines": %d
		}`,
			float64(memStats.Alloc)/1024/1024,
			float64(memStats.TotalAlloc)/1024/1024,
			float64(memStats.Sys)/1024/1024,
			memStats.HeapObjects,
			memStats.NumGC,
			memStats.GCCPUFraction*100,
			float64(memStats.NextGC)/1024/1024,
			float64(memStats.StackSys)/1024/1024,
			runtime.NumGoroutine(),
		)
	})
	
	// Log monitoring endpoint
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		// This would need to be integrated with the logging system
		// For now, return basic log statistics
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"note": "Integrate with logging system to show log statistics",
			"endpoints": {
				"log_stats": "/logs/stats",
				"log_level": "/logs/level",
				"log_sampling": "/logs/sampling"
			}
		}`)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"healthy","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
	})
	
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
	
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Errorf("Failed to shutdown metrics server: %v", err)
		}
	}()
	
	log.Infof("Metrics server starting on port %d", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("metrics server failed: %w", err)
	}
	
	return nil
}
