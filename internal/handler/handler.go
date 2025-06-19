package handler

import (
	"binance-proxy/internal/service"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

func NewHandler(ctx context.Context, class service.Class, enableFakeKline bool, alwaysShowForwards bool) func(w http.ResponseWriter, r *http.Request) {
	handler := &Handler{
		srv:                service.NewService(ctx, class),
		class:              class,
		enableFakeKline:    enableFakeKline,
		alwaysShowForwards: alwaysShowForwards,
	}
	handler.ctx, handler.cancel = context.WithCancel(ctx)

	return handler.Router
}

type Handler struct {
	ctx    context.Context
	cancel context.CancelFunc

	class              service.Class
	srv                *service.Service
	enableFakeKline    bool
	alwaysShowForwards bool
}

func (s *Handler) Router(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Record the request in status tracker
	statusTracker := service.GetStatusTracker()
	statusTracker.RecordRequest()
	switch r.URL.Path {
	case "/status":
		s.status(w, r)

	case "/restart":
		s.restart(w, r)

	case "/api/v3/klines", "/fapi/v1/klines":
		s.klines(w, r)

	case "/api/v3/depth", "/fapi/v1/depth":
		s.depth(w, r)

	case "/api/v3/ticker/24hr":
		s.ticker(w, r)

	case "/api/v3/exchangeInfo", "/fapi/v1/exchangeInfo":
		s.exchangeInfo(w)

	default:
		s.reverseProxy(w, r)
	}
	duration := time.Since(start)
	log.Debugf("%s request %s %s from %s served in %s", s.class, r.Method, r.RequestURI, r.RemoteAddr, duration)
}

// HTTP client with connection pooling for reverse proxy
var (
	proxyHTTPClientOnce sync.Once
	proxyHTTPClient     *http.Client
)

func getProxyHTTPClient() *http.Client {
	proxyHTTPClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			ForceAttemptHTTP2:   true,
			// Connection pooling settings for high throughput
			MaxConnsPerHost: 50,
		}

		if transport == nil {
			log.Errorf("Failed to create HTTP transport, using default")
			transport = http.DefaultTransport.(*http.Transport)
		}

		proxyHTTPClient = &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second, // Longer timeout for proxy requests
		}

		if proxyHTTPClient == nil {
			log.Errorf("Failed to create HTTP client")
			proxyHTTPClient = &http.Client{
				Transport: http.DefaultTransport,
				Timeout:   60 * time.Second,
			}
		}

		if proxyHTTPClient.Transport == nil {
			log.Errorf("Created HTTP client has nil transport, using default transport")
			proxyHTTPClient.Transport = http.DefaultTransport
		}
	})

	if proxyHTTPClient == nil {
		log.Errorf("HTTP client is nil after sync.Once, creating emergency default client")
		return &http.Client{
			Transport: http.DefaultTransport,
			Timeout:   60 * time.Second,
		}
	}

	// Double-check transport is not nil
	if proxyHTTPClient.Transport == nil {
		log.Errorf("HTTP client transport is nil, fixing with default transport")
		proxyHTTPClient.Transport = http.DefaultTransport
	}

	return proxyHTTPClient
}

func (s *Handler) reverseProxy(w http.ResponseWriter, r *http.Request) {
	// Validate handler state
	if s == nil {
		log.Errorf("Handler is nil in reverseProxy")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	if w == nil {
		log.Errorf("ResponseWriter is nil in reverseProxy")
		return
	}
	
	if r == nil {
		log.Errorf("Request is nil in reverseProxy")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Check if context is cancelled
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			log.Warnf("Reverse proxy called but context is cancelled")
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		default:
			// Context is still valid, continue
		}
	}

	// Check if API is banned
	banDetector := service.GetBanDetector()
	if banDetector != nil && banDetector.IsBanned(s.class) {
		banned, recoveryTime := banDetector.GetBanStatus(s.class)
		if banned {
			log.Warnf("%s API is banned, returning empty response. Recovery time: %v", s.class, recoveryTime)
			s.returnEmptyResponse(w, r)
			return
		}
	}

	msg := fmt.Sprintf("%s request %s %s from %s is not cachable", s.class, r.Method, r.RequestURI, r.RemoteAddr)
	if s.alwaysShowForwards {
		log.Info(msg)
	} else {
		log.Trace(msg)
	}

	service.RateWait(s.ctx, s.class, r.Method, r.URL.Path, r.URL.Query())

	// Use hardcoded endpoints (current working version)
	var u *url.URL
	var err error
	if s.class == service.SPOT {
		r.Host = "api.binance.com"
		u, err = url.Parse("https://api.binance.com")
	} else {
		r.Host = "fapi.binance.com"
		u, err = url.Parse("https://fapi.binance.com")
	}
	
	if err != nil || u == nil {
		log.Errorf("Failed to parse URL for %s: %v", s.class, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(u)
	if proxy == nil {
		log.Errorf("Failed to create reverse proxy for %s", s.class)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Use custom HTTP client with connection pooling
	httpClient := getProxyHTTPClient()
	if httpClient == nil {
		log.Errorf("HTTP client is nil, cannot create proxy")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	transport := httpClient.Transport
	if transport == nil {
		log.Errorf("HTTP transport is nil, using default transport")
		transport = http.DefaultTransport
	}

	banTransport := &banCheckTransport{
		Transport: transport,
		class:     s.class,
		handler:   s,
		w:         w,
		r:         r,
	}
	
	// Validate banTransport fields
	if banTransport.handler == nil {
		log.Errorf("Handler is nil in banCheckTransport")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	proxy.Transport = banTransport

	proxy.ServeHTTP(w, r)
}

func (s *Handler) returnEmptyResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "ban-protection")

	var response []byte
	switch r.URL.Path {
	case "/api/v3/klines", "/fapi/v1/klines":
		response = []byte("[]") // Empty klines array
	case "/api/v3/depth", "/fapi/v1/depth":
		response = []byte(`{"lastUpdateId":0,"bids":[],"asks":[]}`)
	case "/api/v3/ticker/24hr":
		response = []byte("{}") // Empty ticker object
	default:
		response = []byte("{}") // Generic empty response
	}

	w.Write(response)
}

type banCheckTransport struct {
	Transport http.RoundTripper
	class     service.Class
	handler   *Handler
	w         http.ResponseWriter
	r         *http.Request
	// Remove endpoint field until load balancer is implemented
}

func (t *banCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Validate essential fields
	if t == nil {
		log.Errorf("banCheckTransport is nil")
		return nil, fmt.Errorf("transport is nil")
	}
	
	if t.handler == nil {
		log.Errorf("Handler is nil in banCheckTransport")
		return nil, fmt.Errorf("handler is nil")
	}
	
	if req == nil {
		log.Errorf("Request is nil in banCheckTransport")
		return nil, fmt.Errorf("request is nil")
	}

	if t.Transport == nil {
		log.Errorf("Transport is nil in banCheckTransport, using default transport")
		t.Transport = http.DefaultTransport
	}

	resp, err := t.Transport.RoundTrip(req)

	// Check for bans
	banDetector := service.GetBanDetector()
	if banDetector != nil && banDetector.CheckResponse(t.class, resp, err) {
		// API is now banned, return empty response instead
		if resp != nil {
			resp.Body.Close()
		}
		
		// Validate response writer before using it
		if t.w != nil && t.r != nil && t.handler != nil {
			t.handler.returnEmptyResponse(t.w, t.r)
		} else {
			log.Errorf("Cannot return empty response: w=%v, r=%v, handler=%v", t.w != nil, t.r != nil, t.handler != nil)
		}
		return nil, nil // Don't return the actual error response
	}

	return resp, err
}

func (s *Handler) status(w http.ResponseWriter, r *http.Request) {
	// Check if context is still valid
	select {
	case <-s.ctx.Done():
		log.Warnf("Status endpoint called but context is canceled")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "service shutting down", "status": "unavailable"}`))
		return
	default:
		// Context is still valid, proceed normally
	}

	// Record the request
	statusTracker := service.GetStatusTracker()
	statusTracker.RecordRequest()

	// Get current status
	status := statusTracker.GetStatus()

	// Add ban information from the existing ban detector
	banDetector := service.GetBanDetector()
	isBanned, recoveryTime := banDetector.GetBanStatus(s.class)
	// Create response with both status and ban info
	response := map[string]interface{}{
		"proxy_status": status,
		"class":        string(s.class),
		"ban_info": map[string]interface{}{
			"banned":        isBanned,
			"recovery_time": nil,
		},
		"config": map[string]interface{}{
			"fake_kline_enabled":   s.enableFakeKline,
			"always_show_forwards": s.alwaysShowForwards,
		},
	}

	if isBanned {
		response["ban_info"].(map[string]interface{})["recovery_time"] = recoveryTime.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Handler) restart(w http.ResponseWriter, r *http.Request) {
	// Security check - only allow GET requests
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error": "only GET method allowed", "status": "failed"}`))
		return
	}

	log.Warnf("RESTART requested from %s for class %s", r.RemoteAddr, s.class)

	// Send immediate response before restart
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"message":   "Restart initiated",
		"status":    "success",
		"class":     string(s.class),
		"timestamp": time.Now().Format(time.RFC3339),
		"warning":   "Service will restart in 2 seconds. This will interrupt all active connections.",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Errorf("Failed to encode restart response: %v", err)
	}

	// Flush the response to ensure it's sent
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Give the response time to be sent
	go func() {
		time.Sleep(2 * time.Second)
		log.Warnf("Executing restart for class %s...", s.class)

		// Cancel the context to trigger graceful shutdown
		s.cancel()

		// Give some time for graceful shutdown, then force exit
		time.Sleep(3 * time.Second)
		log.Fatalf("Force restart for class %s", s.class)
	}()
}
