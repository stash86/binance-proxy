package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"binance-proxy/internal/config"
	"binance-proxy/internal/metrics"
	"binance-proxy/internal/pool"
	"binance-proxy/internal/recovery"
	"binance-proxy/internal/server"
	"binance-proxy/internal/service"

	_ "net/http/pprof"

	log "github.com/sirupsen/logrus"
)

var (
	Version   string = "develop"
	Buildtime string = "undefined"
)

func main() {
	// Memory optimization settings
	optimizeMemorySettings()
	
	// Initialize memory pools early
	pool.InitializePools()
	
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		if err.Error() == "help requested" {
			os.Exit(0)
		}
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logging based on configuration
	if err := cfg.SetupLogging(); err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}

	log.Infof("Binance Proxy version %s, build time %s", Version, Buildtime)
	log.Infof("Configuration: %s", cfg.GetDisplayName())
	
	// Log memory settings
	logMemoryInfo()

	// Create main context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Create server manager
	serverManager := server.NewManager()
	
	// Initialize auto-recovery if enabled
	var autoRecovery *recovery.AutoRecovery
	if cfg.Features.EnableMetrics { // Use metrics flag for now, can add specific recovery flag
		autoRecovery = recovery.NewAutoRecovery(cfg)
		
		// Set recovery callbacks
		autoRecovery.SetCallbacks(
			func() error {
				// Restart callback - restart servers
				log.Warn("Auto-recovery triggered - restarting servers")
				return serverManager.Shutdown() // This will trigger restart via Docker/K8s
			},
			func() bool {
				// Health check callback
				metrics := metrics.GetMetrics().GetSnapshot()
				totalRequests := metrics.TotalRequests
				failedRequests := metrics.FailedRequests
				
				if totalRequests > 100 && failedRequests > totalRequests/5 { // More than 20% error rate
					return false
				}
				return true
			},
		)
		
		autoRecovery.Start()
		defer autoRecovery.Stop()
	}

	// Start metrics server if enabled
	if cfg.Features.EnableMetrics {
		go func() {
			if err := metrics.StartMetricsServer(ctx, cfg.Features.MetricsPort); err != nil {
				log.Errorf("Metrics server failed: %v", err)
			}
		}()
	}

	// Initialize rate limiters with configuration
	service.InitializeRateLimiters(
		cfg.RateLimit.SpotRPS,
		cfg.RateLimit.SpotBurst,
		cfg.RateLimit.FuturesRPS,
		cfg.RateLimit.FuturesBurst,
	)

	// Create and start servers based on configuration
	if !cfg.Markets.DisableSpot {
		spotServer := server.NewServer(ctx, service.SPOT, cfg.Server.SpotPort, cfg)
		serverManager.AddServer(spotServer)
		log.Infof("SPOT market proxy will start on port %d", cfg.Server.SpotPort)
	}

	if !cfg.Markets.DisableFutures {
		futuresServer := server.NewServer(ctx, service.FUTURES, cfg.Server.FuturesPort, cfg)
		serverManager.AddServer(futuresServer)
		log.Infof("FUTURES market proxy will start on port %d", cfg.Server.FuturesPort)
	}

	// Log feature status
	if !cfg.Features.DisableFakeKline {
		log.Info("Fake candles are enabled for faster processing")
	}
	if cfg.Logging.ShowForwards {
		log.Info("Always show forwards is enabled")
	}
	if cfg.Features.EnableMetrics {
		log.Infof("Metrics endpoint available at http://localhost:%d/metrics", cfg.Features.MetricsPort)
		log.Infof("Memory monitoring available at http://localhost:%d/memory", cfg.Features.MetricsPort)
	}

	// Start memory monitoring goroutine
	go monitorMemoryUsage(ctx)

	// Start all servers
	if err := serverManager.Start(); err != nil {
		log.Fatalf("Failed to start servers: %v", err)
	}

	log.Info("Binance Proxy started successfully. Press Ctrl+C to shutdown...")

	// Wait for shutdown signal
	<-shutdown
	log.Info("Shutdown signal received, initiating graceful shutdown...")

	// Cancel context to signal all components to stop
	cancel()

	// Shutdown servers with timeout
	shutdownComplete := make(chan struct{})
	go func() {
		defer close(shutdownComplete)
		if err := serverManager.Shutdown(); err != nil {
			log.Errorf("Error during shutdown: %v", err)
		}
	}()

	// Wait for shutdown to complete or timeout
	select {
	case <-shutdownComplete:
		log.Info("Graceful shutdown completed")
	case <-time.After(35 * time.Second):
		log.Warn("Shutdown timeout exceeded, forcing exit")
	}

	// Final memory cleanup
	runtime.GC()
	debug.FreeOSMemory()

	log.Info("Binance Proxy stopped")
}

// optimizeMemorySettings configures Go runtime for better memory usage
func optimizeMemorySettings() {
	// Set garbage collection target percentage
	debug.SetGCPercent(50) // More aggressive GC (default is 100)
	
	// Set memory limit if available (Go 1.19+)
	if memLimit := os.Getenv("BPX_MEMORY_LIMIT_MB"); memLimit != "" {
		// This would be implemented in newer Go versions
		log.Infof("Memory limit requested: %s MB", memLimit)
	}
	
	// Set GOMAXPROCS to container limits if in container
	if maxProcs := os.Getenv("GOMAXPROCS"); maxProcs == "" {
		// Let runtime detect container limits
		runtime.GOMAXPROCS(0)
	}
	
	// Set initial heap size to reduce allocations
	debug.SetMemoryLimit(128 * 1024 * 1024) // 128MB soft limit
}

// logMemoryInfo logs current memory configuration
func logMemoryInfo() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	
	log.WithFields(log.Fields{
		"gomaxprocs":      runtime.GOMAXPROCS(0),
		"gc_percent":      debug.SetGCPercent(-1), // Get current value
		"initial_heap_mb": memStats.HeapSys / 1024 / 1024,
		"goroutines":      runtime.NumGoroutine(),
	}).Info("Memory optimization settings applied")
	
	// Reset GC percent after reading
	debug.SetGCPercent(50)
}

// monitorMemoryUsage monitors memory usage and triggers cleanup when needed
func monitorMemoryUsage(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	const memoryThreshold = 100 * 1024 * 1024 // 100MB
	const criticalThreshold = 200 * 1024 * 1024 // 200MB
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)
			
			if memStats.Alloc > criticalThreshold {
				log.WithFields(log.Fields{
					"memory_mb":     memStats.Alloc / 1024 / 1024,
					"heap_objects":  memStats.HeapObjects,
					"gc_runs":       memStats.NumGC,
					"goroutines":    runtime.NumGoroutine(),
				}).Warn("Critical memory usage detected, forcing garbage collection")
				
				runtime.GC()
				debug.FreeOSMemory()
				
			} else if memStats.Alloc > memoryThreshold {
				log.WithFields(log.Fields{
					"memory_mb":     memStats.Alloc / 1024 / 1024,
					"heap_objects":  memStats.HeapObjects,
				}).Debug("High memory usage detected")
			}
		}
	}
}
