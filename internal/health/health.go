package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"binance-proxy/internal/metrics"
	"binance-proxy/internal/service"

	log "github.com/sirupsen/logrus"
)

// HealthChecker performs health checks
type HealthChecker struct {
	mu              sync.RWMutex
	services        map[service.Class]*service.Service
	lastHealthCheck time.Time
	healthStatus    HealthStatus
}

// HealthStatus represents the overall health status
type HealthStatus struct {
	Status    string                    `json:"status"`
	Timestamp time.Time                 `json:"timestamp"`
	Uptime    time.Duration             `json:"uptime"`
	Version   string                    `json:"version"`
	Services  map[string]ServiceHealth  `json:"services"`
	Metrics   metrics.MetricsSnapshot   `json:"metrics"`
}

// ServiceHealth represents the health of a specific service
type ServiceHealth struct {
	Status              string    `json:"status"`
	ActiveConnections   int       `json:"active_connections"`
	LastActivity        time.Time `json:"last_activity"`
	ErrorCount          int64     `json:"error_count"`
	ReconnectionCount   int64     `json:"reconnection_count"`
}

var (
	globalHealthChecker *HealthChecker
	healthOnce          sync.Once
	startTime           = time.Now()
	version             = "develop"
)

// GetHealthChecker returns the global health checker instance
func GetHealthChecker() *HealthChecker {
	healthOnce.Do(func() {
		globalHealthChecker = &HealthChecker{
			services:     make(map[service.Class]*service.Service),
			healthStatus: HealthStatus{
				Status:    "unknown",
				Timestamp: time.Now(),
				Services:  make(map[string]ServiceHealth),
			},
		}
	})
	return globalHealthChecker
}

// SetVersion sets the application version for health checks
func SetVersion(v string) {
	version = v
}

// RegisterService registers a service for health checking
func (h *HealthChecker) RegisterService(class service.Class, svc *service.Service) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.services[class] = svc
}

// CheckHealth performs a comprehensive health check
func (h *HealthChecker) CheckHealth(ctx context.Context) HealthStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	h.lastHealthCheck = now

	// Get metrics snapshot
	metricsSnapshot := metrics.GetMetrics().GetSnapshot()

	// Overall status determination
	overallStatus := "healthy"
	
	// Check if we have any critical errors
	if metricsSnapshot.FailedRequests > metricsSnapshot.TotalRequests/10 { // More than 10% error rate
		overallStatus = "degraded"
	}
	
	// Check if we have recent activity
	if metricsSnapshot.TotalRequests == 0 && time.Since(startTime) > 5*time.Minute {
		overallStatus = "idle"
	}

	// Build service health status
	serviceHealth := make(map[string]ServiceHealth)
	
	for class := range h.services {
		// For now, we'll use basic metrics since we don't have direct service health APIs
		health := ServiceHealth{
			Status:            "healthy",
			ActiveConnections: int(metricsSnapshot.ActiveWebSocketConnections),
			LastActivity:      now, // This would be updated by actual service activity
			ErrorCount:        metricsSnapshot.FailedRequests,
			ReconnectionCount: metricsSnapshot.WebSocketReconnections,
		}
		
		serviceHealth[string(class)] = health
	}

	h.healthStatus = HealthStatus{
		Status:    overallStatus,
		Timestamp: now,
		Uptime:    time.Since(startTime),
		Version:   version,
		Services:  serviceHealth,
		Metrics:   metricsSnapshot,
	}

	return h.healthStatus
}

// GetLastHealthStatus returns the last health check result
func (h *HealthChecker) GetLastHealthStatus() HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.healthStatus
}

// HTTPHandler returns an HTTP handler for health checks
func (h *HealthChecker) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		health := h.CheckHealth(ctx)

		w.Header().Set("Content-Type", "application/json")
		
		// Set HTTP status based on health
		switch health.Status {
		case "healthy", "idle":
			w.WriteHeader(http.StatusOK)
		case "degraded":
			w.WriteHeader(http.StatusPartialContent) // 206
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		if err := json.NewEncoder(w).Encode(health); err != nil {
			log.Errorf("Failed to encode health status: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
}

// ReadinessHandler returns an HTTP handler for readiness checks
func (h *HealthChecker) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Simple readiness check - are we accepting connections?
		status := h.GetLastHealthStatus()
		
		ready := status.Status == "healthy" || status.Status == "idle"
		
		w.Header().Set("Content-Type", "application/json")
		
		if ready {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":     true,
				"timestamp": time.Now(),
				"status":    status.Status,
			})
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":     false,
				"timestamp": time.Now(),
				"status":    status.Status,
				"reason":    "Service not ready",
			})
		}
	}
}

// LivenessHandler returns an HTTP handler for liveness checks
func (h *HealthChecker) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Simple liveness check - is the process running?
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		json.NewEncoder(w).Encode(map[string]interface{}{
			"alive":     true,
			"timestamp": time.Now(),
			"uptime":    time.Since(startTime).String(),
		})
	}
}
