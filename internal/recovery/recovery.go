package recovery

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"binance-proxy/internal/config"
	"binance-proxy/internal/metrics"

	log "github.com/sirupsen/logrus"
)

// AutoRecovery manages automatic restart/recovery of services with memory optimization
type AutoRecovery struct {
	mu                sync.RWMutex
	config            *config.Config
	errorThreshold    int64
	errorWindow       time.Duration
	restartCooldown   time.Duration
	
	// Memory-efficient error tracking with circular buffer
	errorBuffer       []errorEvent
	bufferSize        int
	bufferIndex       int64
	windowStart       int64 // Unix timestamp for memory efficiency
	lastRestart       int64 // Unix timestamp for memory efficiency
	restartCount      int64
	
	// Recovery callbacks
	onRestart         func() error
	onHealthCheck     func() bool
	
	// Control
	enabled           bool
	ctx               context.Context
	cancel            context.CancelFunc
	ticker            *time.Ticker
	
	// Memory management
	memoryThreshold   uint64 // Memory usage threshold in bytes
	gcInterval        time.Duration
	lastGC            int64
}

// errorEvent represents a memory-efficient error event
type errorEvent struct {
	timestamp int64  // Unix timestamp (8 bytes vs 24 bytes for time.Time)
	errorType uint8  // Error type enum (1 byte vs string)
}

// Error type enumeration for memory efficiency
const (
	ErrorTypeHTTP uint8 = iota
	ErrorTypeWebSocket
	ErrorTypeRateLimit
	ErrorTypeTimeout
	ErrorTypeContext
	ErrorTypeGeneric
)

// getErrorType converts error string to enum for memory efficiency
func getErrorType(errorStr string) uint8 {
	switch errorStr {
	case "http", "proxy_error":
		return ErrorTypeHTTP
	case "websocket", "ws_error":
		return ErrorTypeWebSocket
	case "rate_limit":
		return ErrorTypeRateLimit
	case "timeout":
		return ErrorTypeTimeout
	case "context_canceled", "context":
		return ErrorTypeContext
	default:
		return ErrorTypeGeneric
	}
}

// NewAutoRecovery creates a new auto-recovery manager with memory optimization
func NewAutoRecovery(cfg *config.Config) *AutoRecovery {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Use smaller buffer size for memory efficiency
	bufferSize := 100 // Instead of keeping unlimited errors
	
	return &AutoRecovery{
		config:          cfg,
		errorThreshold:  10,  // 10 errors in window
		errorWindow:     5 * time.Minute,
		restartCooldown: 2 * time.Minute,
		enabled:         true,
		ctx:             ctx,
		cancel:          cancel,
		windowStart:     time.Now().Unix(),
		
		// Memory-efficient circular buffer
		errorBuffer:     make([]errorEvent, bufferSize),
		bufferSize:      bufferSize,
		bufferIndex:     0,
		
		// Memory management settings
		memoryThreshold: 100 * 1024 * 1024, // 100MB threshold
		gcInterval:      5 * time.Minute,
		lastGC:          time.Now().Unix(),
	}
}

// SetCallbacks sets the recovery callbacks
func (ar *AutoRecovery) SetCallbacks(onRestart func() error, onHealthCheck func() bool) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	
	ar.onRestart = onRestart
	ar.onHealthCheck = onHealthCheck
}

// Start begins the auto-recovery monitoring with memory management
func (ar *AutoRecovery) Start() {
	if !ar.enabled {
		return
	}
	
	ar.ticker = time.NewTicker(30 * time.Second)
	
	go func() {
		defer ar.ticker.Stop()
		
		for {
			select {
			case <-ar.ctx.Done():
				return
			case <-ar.ticker.C:
				ar.checkAndRecover()
				ar.performMemoryMaintenance()
			}
		}
	}()
	
	log.Info("Auto-recovery monitoring started with memory optimization")
}

// performMemoryMaintenance handles memory cleanup and garbage collection
func (ar *AutoRecovery) performMemoryMaintenance() {
	now := time.Now().Unix()
	
	// Perform GC if needed
	if now-ar.lastGC > int64(ar.gcInterval.Seconds()) {
		ar.cleanupOldErrors()
		
		// Check memory usage
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		
		if memStats.Alloc > ar.memoryThreshold {
			log.WithFields(log.Fields{
				"memory_used_mb": memStats.Alloc / 1024 / 1024,
				"threshold_mb":   ar.memoryThreshold / 1024 / 1024,
			}).Warn("High memory usage detected, forcing garbage collection")
			
			runtime.GC()
			runtime.ReadMemStats(&memStats)
			
			log.WithFields(log.Fields{
				"memory_used_mb_after_gc": memStats.Alloc / 1024 / 1024,
			}).Info("Garbage collection completed")
		}
		
		ar.lastGC = now
	}
}

// cleanupOldErrors removes old errors from the circular buffer
func (ar *AutoRecovery) cleanupOldErrors() {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	
	now := time.Now().Unix()
	windowStart := now - int64(ar.errorWindow.Seconds())
	
	// Reset buffer if all errors are too old
	oldCount := 0
	for i := 0; i < ar.bufferSize; i++ {
		if ar.errorBuffer[i].timestamp != 0 && ar.errorBuffer[i].timestamp < windowStart {
			oldCount++
		}
	}
	
	if oldCount > ar.bufferSize/2 {
		// Clear old entries to free memory
		for i := 0; i < ar.bufferSize; i++ {
			if ar.errorBuffer[i].timestamp < windowStart {
				ar.errorBuffer[i] = errorEvent{} // Zero value to free memory
			}
		}
		
		log.WithFields(log.Fields{
			"cleaned_errors": oldCount,
			"total_buffer":   ar.bufferSize,
		}).Debug("Cleaned up old error events from memory")
	}
}

// Stop stops the auto-recovery monitoring
func (ar *AutoRecovery) Stop() {
	ar.cancel()
	if ar.ticker != nil {
		ar.ticker.Stop()
	}
	log.Info("Auto-recovery monitoring stopped")
}

// RecordError records an error using memory-efficient circular buffer
func (ar *AutoRecovery) RecordError(errorType string) {
	if !ar.enabled {
		return
	}
	
	now := time.Now().Unix()
	errorTypeEnum := getErrorType(errorType)
	
	ar.mu.Lock()
	defer ar.mu.Unlock()
	
	// Use circular buffer to limit memory usage
	index := atomic.AddInt64(&ar.bufferIndex, 1) % int64(ar.bufferSize)
	ar.errorBuffer[index] = errorEvent{
		timestamp: now,
		errorType: errorTypeEnum,
	}
	
	log.WithFields(log.Fields{
		"error_type":    errorType,
		"buffer_index":  index,
		"timestamp":     now,
	}).Debug("Error recorded in circular buffer")
}

// countRecentErrors counts errors in the current window efficiently
func (ar *AutoRecovery) countRecentErrors() int64 {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	
	now := time.Now().Unix()
	windowStart := now - int64(ar.errorWindow.Seconds())
	
	var count int64
	for i := 0; i < ar.bufferSize; i++ {
		event := ar.errorBuffer[i]
		if event.timestamp != 0 && event.timestamp >= windowStart {
			count++
		}
	}
	
	return count
}

// checkAndRecover checks if recovery is needed and performs it
func (ar *AutoRecovery) checkAndRecover() {
	errorCount := ar.countRecentErrors()
	now := time.Now().Unix()
	timeSinceRestart := now - atomic.LoadInt64(&ar.lastRestart)
	
	// Check if we should recover
	shouldRecover := false
	reason := ""
	
	if errorCount >= ar.errorThreshold {
		shouldRecover = true
		reason = "error threshold exceeded"
	}
	
	// Check health if callback is available
	if ar.onHealthCheck != nil && !ar.onHealthCheck() {
		shouldRecover = true
		reason = "health check failed"
	}
	
	// Respect cooldown period
	if shouldRecover && timeSinceRestart < int64(ar.restartCooldown.Seconds()) {
		log.WithFields(log.Fields{
			"reason":              reason,
			"time_since_restart":  timeSinceRestart,
			"cooldown_remaining":  int64(ar.restartCooldown.Seconds()) - timeSinceRestart,
		}).Warn("Recovery needed but in cooldown period")
		return
	}
	
	if shouldRecover {
		ar.performRecovery(reason, errorCount)
	}
}

// performRecovery performs the actual recovery/restart with memory cleanup
func (ar *AutoRecovery) performRecovery(reason string, errorCount int64) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	
	restartCount := atomic.AddInt64(&ar.restartCount, 1)
	now := time.Now().Unix()
	atomic.StoreInt64(&ar.lastRestart, now)
	
	log.WithFields(log.Fields{
		"reason":        reason,
		"restart_count": restartCount,
		"error_count":   errorCount,
	}).Warn("Performing auto-recovery")
	
	// Clear error buffer to free memory after restart
	for i := 0; i < ar.bufferSize; i++ {
		ar.errorBuffer[i] = errorEvent{}
	}
	atomic.StoreInt64(&ar.bufferIndex, 0)
	
	// Record metrics
	metrics.GetMetrics().RecordError("auto_recovery_triggered")
	
	// Force garbage collection before restart to free memory
	runtime.GC()
	
	// Perform restart if callback is available
	if ar.onRestart != nil {
		if err := ar.onRestart(); err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("Auto-recovery restart failed")
		} else {
			log.Info("Auto-recovery restart completed successfully")
		}
	}
}

// GetStats returns recovery statistics with memory usage info
func (ar *AutoRecovery) GetStats() map[string]interface{} {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	
	// Get memory statistics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	// Count recent errors efficiently
	recentErrors := ar.countRecentErrors()
	
	return map[string]interface{}{
		"enabled":              ar.enabled,
		"recent_error_count":   recentErrors,
		"restart_count":        atomic.LoadInt64(&ar.restartCount),
		"last_restart":         time.Unix(atomic.LoadInt64(&ar.lastRestart), 0),
		"window_start":         time.Unix(atomic.LoadInt64(&ar.windowStart), 0),
		"error_threshold":      ar.errorThreshold,
		"error_window_seconds": ar.errorWindow.Seconds(),
		"cooldown_seconds":     ar.restartCooldown.Seconds(),
		"buffer_size":          ar.bufferSize,
		"buffer_index":         atomic.LoadInt64(&ar.bufferIndex) % int64(ar.bufferSize),
		
		// Memory statistics
		"memory_stats": map[string]interface{}{
			"alloc_mb":        memStats.Alloc / 1024 / 1024,
			"total_alloc_mb":  memStats.TotalAlloc / 1024 / 1024,
			"sys_mb":          memStats.Sys / 1024 / 1024,
			"num_gc":          memStats.NumGC,
			"gc_cpu_percent":  memStats.GCCPUFraction * 100,
			"heap_objects":    memStats.HeapObjects,
		},
	}
}

// Stop stops the auto-recovery monitoring and cleans up memory
func (ar *AutoRecovery) Stop() {
	ar.cancel()
	if ar.ticker != nil {
		ar.ticker.Stop()
	}
	
	// Clean up memory
	ar.mu.Lock()
	ar.errorBuffer = nil // Release buffer memory
	ar.mu.Unlock()
	
	// Force garbage collection on shutdown
	runtime.GC()
	
	log.Info("Auto-recovery monitoring stopped and memory cleaned up")
}
