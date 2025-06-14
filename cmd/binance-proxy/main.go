package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"binance-proxy/internal/cache"
	"binance-proxy/internal/config"
	"binance-proxy/internal/environments"
	"binance-proxy/internal/health"
	"binance-proxy/internal/logging"
	"binance-proxy/internal/metrics"
	"binance-proxy/internal/monitoring"
	"binance-proxy/internal/performance"
	"binance-proxy/internal/pool"
	"binance-proxy/internal/recovery"
	"binance-proxy/internal/security"
	"binance-proxy/internal/server"
	"binance-proxy/internal/service"
	"binance-proxy/internal/throttle"
	"binance-proxy/internal/websocket"

	"net/http"
	_ "net/http/pprof"

	log "github.com/sirupsen/logrus"
)

var (
	Version   string = "develop"
	Buildtime string = "undefined"
)

func main() {
	// Detect and configure environment
	currentEnv := environments.GetEnvironment()
	envConfig := environments.GetEnvironmentConfig(currentEnv)
	log.Infof("Running in %s environment", currentEnv)

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		if err.Error() == "help requested" {
			os.Exit(0)
		}
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Apply environment-specific overrides
	environments.ApplyEnvironmentOverrides(cfg, envConfig)

	// Setup logging based on configuration
	if err := cfg.SetupLogging(); err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}

	// Initialize advanced logging system
	logManager, err := logging.NewManager(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize log manager: %v", err)
	}
	defer logManager.Close()

	log.Infof("Binance Proxy version %s, build time %s", Version, Buildtime)
	log.Infof("Configuration: %s", cfg.GetDisplayName())

	// Initialize performance tuner
	perfConfig := &performance.Config{
		EnableGCTuning:       envConfig.Name == environments.Production || envConfig.Name == environments.Staging,
		GCPercent:            envConfig.Limits.GCPercent,
		MemoryLimit:          uint64(envConfig.Limits.MaxMemoryMB),
		OptimizationInterval: 30 * time.Second,
		EnableBallastMemory:  envConfig.Name == environments.Production,
		BallastSizeMB:        64,
	}
	perfTuner := performance.NewTuner(context.Background(), perfConfig)
	defer perfTuner.Stop()

	// Initialize memory pools for optimization
	poolManager := pool.NewManager()
	defer poolManager.Close()

	// Initialize recovery system
	recoveryManager := recovery.NewManager(cfg)
	defer recoveryManager.Stop()

	// Initialize advanced throttling system
	throttleConfig := &throttle.Config{
		BaseRPS:         envConfig.Limits.RateLimitRPS,
		BaseBurst:       int(envConfig.Limits.RateLimitRPS * 2),
		MaxRPS:          envConfig.Limits.RateLimitRPS * 3,
		MinRPS:          envConfig.Limits.RateLimitRPS * 0.1,
		SuccessWindow:   1 * time.Minute,
		ErrorWindow:     5 * time.Minute,
		CleanupInterval: 10 * time.Minute,
		AdaptiveEnabled: envConfig.Features.EnableRateLimits,
	}
	throttler := throttle.NewAdaptiveThrottler(context.Background(), throttleConfig)
	defer throttler.Stop()

	// Initialize cache system
	cacheManager := cache.NewManager(cfg)
	defer cacheManager.Close()
	log.Infof("Cache system initialized with %d MB memory limit", cfg.Cache.MaxMemoryMB)

	// Initialize security manager
	securityManager := security.NewManager(cfg)
	log.Infof("Security system initialized - API key auth: %v, Rate limiting: %v",
		cfg.Security.EnableAPIKeyAuth, cfg.Security.EnableRateLimit)

	// Initialize WebSocket manager
	wsManager := websocket.NewManager(cfg)
	defer wsManager.Close()
	// Initialize health monitoring
	healthMonitor := health.NewMonitor(cfg)
	defer healthMonitor.Stop()

	// Initialize comprehensive monitoring
	systemMonitor := monitoring.NewMonitor(ctx, securityManager, cacheManager, wsManager)
	defer systemMonitor.Stop()

	// Create main context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Create server manager
	serverManager := server.NewManager()

	// Start metrics server if enabled
	if cfg.Features.EnableMetrics {
		go func() {
			if err := metrics.StartMetricsServer(ctx, cfg.Features.MetricsPort); err != nil {
				log.Errorf("Metrics server failed: %v", err)
			}
		}()

		// Add monitoring endpoints to metrics server
		http.HandleFunc("/stats", systemMonitor.HandleSystemStats(Version))
		http.HandleFunc("/health", systemMonitor.HandleHealthCheck())
		http.HandleFunc("/ready", systemMonitor.HandleReadiness())

		log.Infof("Enhanced monitoring endpoints available:")
		log.Infof("  - Metrics: http://localhost:%d/metrics", cfg.Features.MetricsPort)
		log.Infof("  - System Stats: http://localhost:%d/stats", cfg.Features.MetricsPort)
		log.Infof("  - Health Check: http://localhost:%d/health", cfg.Features.MetricsPort)
		log.Infof("  - Readiness: http://localhost:%d/ready", cfg.Features.MetricsPort)
	}
	// Create and start servers based on configuration
	if !cfg.Markets.DisableSpot {
		spotServer := server.NewServer(ctx, service.SPOT, cfg.Server.SpotPort, cfg,
			securityManager, cacheManager, wsManager)
		serverManager.AddServer(spotServer)
		log.Infof("SPOT market proxy will start on port %d", cfg.Server.SpotPort)
	}

	if !cfg.Markets.DisableFutures {
		futuresServer := server.NewServer(ctx, service.FUTURES, cfg.Server.FuturesPort, cfg,
			securityManager, cacheManager, wsManager)
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
	}

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

	log.Info("Binance Proxy stopped")
}
