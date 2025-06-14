package performance

import (
	"context"
	"runtime"
	"runtime/debug"
	"time"

	log "github.com/sirupsen/logrus"
)

// Tuner provides performance optimization capabilities
type Tuner struct {
	ctx        context.Context
	cancel     context.CancelFunc
	gcPercent  int
	memLimit   uint64
	ticker     *time.Ticker
	enabled    bool
}

// Config holds performance tuning configuration
type Config struct {
	EnableGCTuning       bool          `long:"enable-gc-tuning" env:"ENABLE_GC_TUNING" description:"Enable automatic GC tuning"`
	GCPercent            int           `long:"gc-percent" env:"GC_PERCENT" description:"GC target percentage" default:"100"`
	MemoryLimit          uint64        `long:"memory-limit-mb" env:"MEMORY_LIMIT_MB" description:"Memory limit in MB" default:"512"`
	OptimizationInterval time.Duration `long:"optimization-interval" env:"OPTIMIZATION_INTERVAL" description:"Performance optimization interval" default:"30s"`
	EnableBallastMemory  bool          `long:"enable-ballast-memory" env:"ENABLE_BALLAST_MEMORY" description:"Enable memory ballast for GC optimization"`
	BallastSizeMB        int           `long:"ballast-size-mb" env:"BALLAST_SIZE_MB" description:"Memory ballast size in MB" default:"64"`
}

// NewTuner creates a new performance tuner
func NewTuner(ctx context.Context, config *Config) *Tuner {
	tunerCtx, cancel := context.WithCancel(ctx)
	
	tuner := &Tuner{
		ctx:       tunerCtx,
		cancel:    cancel,
		gcPercent: config.GCPercent,
		memLimit:  config.MemoryLimit * 1024 * 1024, // Convert MB to bytes
		enabled:   config.EnableGCTuning,
	}
	
	if config.EnableGCTuning {
		tuner.ticker = time.NewTicker(config.OptimizationInterval)
		go tuner.optimizationLoop()
	}
	
	// Set initial GC percent
	if config.EnableGCTuning {
		debug.SetGCPercent(config.GCPercent)
		log.Infof("Performance tuner initialized - GC percent: %d, Memory limit: %d MB", 
			config.GCPercent, config.MemoryLimit)
	}
	
	// Enable memory ballast if configured
	if config.EnableBallastMemory && config.BallastSizeMB > 0 {
		tuner.setupMemoryBallast(config.BallastSizeMB)
	}
	
	return tuner
}

// Stop stops the performance tuner
func (t *Tuner) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.ticker != nil {
		t.ticker.Stop()
	}
}

// optimizationLoop runs periodic performance optimizations
func (t *Tuner) optimizationLoop() {
	defer t.ticker.Stop()
	
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-t.ticker.C:
			t.optimize()
		}
	}
}

// optimize performs performance optimizations
func (t *Tuner) optimize() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	// Check memory usage and adjust GC if needed
	memUsageMB := float64(memStats.Alloc) / (1024 * 1024)
	memLimitMB := float64(t.memLimit) / (1024 * 1024)
	
	// Dynamic GC tuning based on memory pressure
	if memUsageMB > memLimitMB*0.8 {
		// High memory pressure - more aggressive GC
		newGCPercent := max(50, t.gcPercent-20)
		debug.SetGCPercent(newGCPercent)
		log.Debugf("High memory pressure (%.1f MB/%.1f MB) - reducing GC percent to %d", 
			memUsageMB, memLimitMB, newGCPercent)
		
		// Force GC if we're very close to limit
		if memUsageMB > memLimitMB*0.95 {
			runtime.GC()
			log.Debugf("Forced GC due to critical memory usage")
		}
	} else if memUsageMB < memLimitMB*0.5 {
		// Low memory pressure - less aggressive GC
		newGCPercent := min(200, t.gcPercent+20)
		debug.SetGCPercent(newGCPercent)
		log.Debugf("Low memory pressure (%.1f MB/%.1f MB) - increasing GC percent to %d", 
			memUsageMB, memLimitMB, newGCPercent)
	}
	
	// Log memory stats periodically
	log.Debugf("Memory stats - Alloc: %.1f MB, Sys: %.1f MB, GC runs: %d, Goroutines: %d",
		float64(memStats.Alloc)/(1024*1024),
		float64(memStats.Sys)/(1024*1024),
		memStats.NumGC,
		runtime.NumGoroutine())
}

// setupMemoryBallast creates a memory ballast to improve GC performance
func (t *Tuner) setupMemoryBallast(sizeMB int) {
	// Create a large slice to act as memory ballast
	// This helps reduce GC frequency by increasing the heap size
	ballastSize := sizeMB * 1024 * 1024
	ballast := make([]byte, ballastSize)
	
	// Prevent the ballast from being optimized away
	runtime.KeepAlive(ballast)
	
	log.Infof("Memory ballast of %d MB created for GC optimization", sizeMB)
}

// GetStats returns performance statistics
func (t *Tuner) GetStats() map[string]interface{} {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	return map[string]interface{}{
		"enabled":           t.enabled,
		"gc_percent":        debug.SetGCPercent(-1), // Get current GC percent
		"memory_limit_mb":   t.memLimit / (1024 * 1024),
		"memory_alloc_mb":   float64(memStats.Alloc) / (1024 * 1024),
		"memory_sys_mb":     float64(memStats.Sys) / (1024 * 1024),
		"gc_runs":           memStats.NumGC,
		"last_gc":           time.Unix(0, int64(memStats.LastGC)).Format(time.RFC3339),
		"goroutines":        runtime.NumGoroutine(),
		"cpu_count":         runtime.NumCPU(),
	}
}

// ForceOptimization forces an immediate optimization cycle
func (t *Tuner) ForceOptimization() {
	if t.enabled {
		t.optimize()
	}
}

// Helper functions
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
