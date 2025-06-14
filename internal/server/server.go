package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"binance-proxy/internal/cache"
	"binance-proxy/internal/config"
	"binance-proxy/internal/handler"
	"binance-proxy/internal/metrics"
	"binance-proxy/internal/security"
	"binance-proxy/internal/service"
	"binance-proxy/internal/websocket"

	log "github.com/sirupsen/logrus"
)

// Server represents an HTTP server instance
type Server struct {
	httpServer      *http.Server
	class           service.Class
	port            int
	config          *config.Config
	securityManager *security.Manager
	cacheManager    *cache.Manager
	wsManager       *websocket.Manager
	shutdown        chan struct{}
	wg              sync.WaitGroup
}

// NewServer creates a new server instance
func NewServer(ctx context.Context, class service.Class, port int, cfg *config.Config,
	securityManager *security.Manager, cacheManager *cache.Manager, wsManager *websocket.Manager) *Server {
	server := &Server{
		class:           class,
		port:            port,
		config:          cfg,
		securityManager: securityManager,
		cacheManager:    cacheManager,
		wsManager:       wsManager,
		shutdown:        make(chan struct{}),
	}
	
	// Create HTTP server with timeouts
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.requestHandler(ctx))
	
	server.httpServer = &http.Server{
		Addr:           fmt.Sprintf(":%d", port),
		Handler:        server.withMiddleware(mux),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderSize,
	}
	
	return server
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		
		log.Infof("%s websocket proxy starting on port %d", s.class, s.port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("%s websocket proxy failed to start: %v", s.class, err)
		}
	}()
	
	// Wait for shutdown signal
	go func() {
		<-ctx.Done()
		s.Shutdown()
	}()
	
	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	close(s.shutdown)
	
	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	log.Infof("%s websocket proxy shutting down gracefully...", s.class)
	
	// Shutdown HTTP server
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorf("%s websocket proxy shutdown error: %v", s.class, err)
		return err
	}
	
	// Wait for all goroutines to finish
	s.wg.Wait()
	
	log.Infof("%s websocket proxy shutdown complete", s.class)
	return nil
}

// requestHandler creates the main request handler
func (s *Server) requestHandler(ctx context.Context) http.HandlerFunc {
	return handler.NewHandler(
		ctx,
		s.class,
		!s.config.Features.DisableFakeKline,
		s.config.Logging.ShowForwards,
	)
}

// withMiddleware adds middleware to the handler
func (s *Server) withMiddleware(handler http.Handler) http.Handler {
	return s.loggingMiddleware(
		s.metricsMiddleware(
			s.cacheMiddleware(
				s.securityMiddleware(
					s.recoveryMiddleware(
						s.corsMiddleware(handler),
					),
				),
			),
		),
	)
}

// loggingMiddleware logs all requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response writer wrapper to capture status code
		ww := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
		
		next.ServeHTTP(ww, r)
		
		duration := time.Since(start)
		
		log.WithFields(log.Fields{
			"method":     r.Method,
			"url":        r.RequestURI,
			"remote":     r.RemoteAddr,
			"status":     ww.statusCode,
			"duration":   duration,
			"user_agent": r.UserAgent(),
			"class":      s.class,
		}).Info("Request processed")
	})
}

// metricsMiddleware records metrics for all requests
func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriterWrapper{ResponseWriter: w, statusCode: 200}
		
		next.ServeHTTP(ww, r)
		
		duration := time.Since(start)
		cached := w.Header().Get("Data-Source") == "websocket" || w.Header().Get("Data-Source") == "apicache"
		
		metrics.GetMetrics().RecordRequest(r.URL.Path, cached, duration)
		
		if ww.statusCode >= 400 {
			metrics.GetMetrics().RecordError(fmt.Sprintf("http_%d", ww.statusCode))
		}
	})
}

// recoveryMiddleware recovers from panics
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.WithFields(log.Fields{
					"error":  err,
					"method": r.Method,
					"url":    r.RequestURI,
					"remote": r.RemoteAddr,
					"class":  s.class,
				}).Error("Panic recovered")
				
				metrics.GetMetrics().RecordError("panic")
				
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.Security.EnableCORS {
			// Use configured CORS settings
			if len(s.config.Security.CORSOrigins) > 0 {
				w.Header().Set("Access-Control-Allow-Origin", s.config.Security.CORSOrigins[0])
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
			
			if len(s.config.Security.CORSMethods) > 0 {
				w.Header().Set("Access-Control-Allow-Methods", 
					fmt.Sprintf("%v", s.config.Security.CORSMethods))
			} else {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			}
			
			if len(s.config.Security.CORSHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", 
					fmt.Sprintf("%v", s.config.Security.CORSHeaders))
			} else {
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
			}
		} else {
			// Default CORS for backward compatibility
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// securityMiddleware applies security checks
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply security checks if enabled
		if s.securityManager != nil {
			if !s.securityManager.ValidateRequest(r) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				metrics.GetMetrics().RecordError("unauthorized")
				return
			}
		}
		
		next.ServeHTTP(w, r)
	})
}

// cacheMiddleware implements caching logic
func (s *Server) cacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check cache for GET requests
		if r.Method == "GET" && s.cacheManager != nil {
			cacheKey := s.generateCacheKey(r)
			
			if cachedData, found := s.cacheManager.Get(cacheKey); found {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Data-Source", "cache")
				w.Write(cachedData)
				metrics.GetMetrics().RecordRequest(r.URL.Path, true, 0)
				return
			}
		}
		
		next.ServeHTTP(w, r)
	})
}

// generateCacheKey creates a cache key for the request
func (s *Server) generateCacheKey(r *http.Request) string {
	return fmt.Sprintf("%s:%s:%s", s.class, r.Method, r.URL.String())
}
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// responseWriterWrapper wraps http.ResponseWriter to capture status code
type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Manager manages multiple server instances
type Manager struct {
	servers []*Server
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewManager creates a new server manager
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		ctx:    ctx,
		cancel: cancel,
	}
}

// AddServer adds a server to the manager
func (m *Manager) AddServer(server *Server) {
	m.servers = append(m.servers, server)
}

// Start starts all servers
func (m *Manager) Start() error {
	for _, server := range m.servers {
		m.wg.Add(1)
		go func(s *Server) {
			defer m.wg.Done()
			if err := s.Start(m.ctx); err != nil {
				log.Errorf("Server failed to start: %v", err)
			}
		}(server)
	}
	
	return nil
}

// Shutdown gracefully shuts down all servers
func (m *Manager) Shutdown() error {
	log.Info("Shutting down all servers...")
	
	m.cancel()
	
	// Shutdown all servers concurrently
	var shutdownWg sync.WaitGroup
	for _, server := range m.servers {
		shutdownWg.Add(1)
		go func(s *Server) {
			defer shutdownWg.Done()
			if err := s.Shutdown(); err != nil {
				log.Errorf("Server shutdown error: %v", err)
			}
		}(server)
	}
	
	// Wait for all servers to shutdown
	shutdownWg.Wait()
	
	// Wait for all server goroutines to finish
	m.wg.Wait()
	
	log.Info("All servers shutdown complete")
	return nil
}
