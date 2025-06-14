package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"binance-proxy/internal/cache"
	"binance-proxy/internal/metrics"
	"binance-proxy/internal/security"
	"binance-proxy/internal/websocket"

	log "github.com/sirupsen/logrus"
)

// Monitor provides comprehensive system monitoring
type Monitor struct {
	ctx             context.Context
	cancel          context.CancelFunc
	securityManager *security.Manager
	cacheManager    *cache.Manager
	wsManager       *websocket.Manager
	startTime       time.Time
}

// NewMonitor creates a new monitoring instance
func NewMonitor(ctx context.Context, securityManager *security.Manager, 
	cacheManager *cache.Manager, wsManager *websocket.Manager) *Monitor {
	monitorCtx, cancel := context.WithCancel(ctx)
	
	return &Monitor{
		ctx:             monitorCtx,
		cancel:          cancel,
		securityManager: securityManager,
		cacheManager:    cacheManager,
		wsManager:       wsManager,
		startTime:       time.Now(),
	}
}

// Stop stops the monitor
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// SystemStats represents comprehensive system statistics
type SystemStats struct {
	// System info
	Uptime      string            `json:"uptime"`
	StartTime   time.Time         `json:"start_time"`
	Version     string            `json:"version"`
	GoVersion   string            `json:"go_version"`
	
	// Runtime stats
	Runtime     RuntimeStats      `json:"runtime"`
	
	// Application stats
	Metrics     *metrics.Stats    `json:"metrics,omitempty"`
	Cache       *cache.Stats      `json:"cache,omitempty"`
	Security    *security.Stats   `json:"security,omitempty"`
	WebSocket   *websocket.Stats  `json:"websocket,omitempty"`
}

// RuntimeStats represents Go runtime statistics
type RuntimeStats struct {
	Goroutines     int     `json:"goroutines"`
	CPUs           int     `json:"cpus"`
	MemoryUsed     int64   `json:"memory_used_bytes"`
	MemoryTotal    int64   `json:"memory_total_bytes"`
	MemoryPercent  float64 `json:"memory_percent"`
	GCRuns         uint32  `json:"gc_runs"`
	LastGC         string  `json:"last_gc"`
}

// GetSystemStats returns comprehensive system statistics
func (m *Monitor) GetSystemStats(version string) *SystemStats {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	stats := &SystemStats{
		Uptime:    time.Since(m.startTime).String(),
		StartTime: m.startTime,
		Version:   version,
		GoVersion: runtime.Version(),
		Runtime: RuntimeStats{
			Goroutines:    runtime.NumGoroutine(),
			CPUs:          runtime.NumCPU(),
			MemoryUsed:    int64(memStats.Alloc),
			MemoryTotal:   int64(memStats.Sys),
			MemoryPercent: float64(memStats.Alloc) / float64(memStats.Sys) * 100,
			GCRuns:        memStats.NumGC,
			LastGC:        time.Unix(0, int64(memStats.LastGC)).Format(time.RFC3339),
		},
	}
	
	// Get metrics stats
	if metricsInstance := metrics.GetMetrics(); metricsInstance != nil {
		stats.Metrics = metricsInstance.GetStats()
	}
	
	// Get cache stats
	if m.cacheManager != nil {
		stats.Cache = m.cacheManager.GetStats()
	}
	
	// Get security stats
	if m.securityManager != nil {
		stats.Security = m.securityManager.GetStats()
	}
	
	// Get WebSocket stats
	if m.wsManager != nil {
		stats.WebSocket = m.wsManager.GetStats()
	}
	
	return stats
}

// HandleSystemStats provides an HTTP handler for system statistics
func (m *Monitor) HandleSystemStats(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := m.GetSystemStats(version)
		
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(stats); err != nil {
			log.Errorf("Failed to encode system stats: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// HandleHealthCheck provides a simple health check endpoint
func (m *Monitor) HandleHealthCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		health := map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
			"uptime":    time.Since(m.startTime).String(),
		}
		
		// Check component health
		checks := make(map[string]bool)
		
		if m.cacheManager != nil {
			checks["cache"] = m.cacheManager.IsHealthy()
		}
		
		if m.securityManager != nil {
			checks["security"] = m.securityManager.IsHealthy()
		}
		
		if m.wsManager != nil {
			checks["websocket"] = m.wsManager.IsHealthy()
		}
		
		health["checks"] = checks
		
		// Determine overall health
		allHealthy := true
		for _, healthy := range checks {
			if !healthy {
				allHealthy = false
				break
			}
		}
		
		if !allHealthy {
			health["status"] = "degraded"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(health)
	}
}

// HandleReadiness provides a readiness check endpoint
func (m *Monitor) HandleReadiness() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		readiness := map[string]interface{}{
			"status":    "ready",
			"timestamp": time.Now().UTC(),
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(readiness)
	}
}
