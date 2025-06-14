package handler

import (
	"binance-proxy/internal/service"
	"bytes"
	"context"
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

	switch r.URL.Path {
	case "/api/v3/klines", "/fapi/v1/klines":
		s.klines(w, r)

	case "/api/v3/depth", "/fapi/v1/depth":
		s.depth(w, r)

	case "/api/v3/ticker/24hr":
		s.ticker(w, r)

	case "/api/v3/exchangeInfo", "/fapi/v1/exchangeInfo":
		s.exchangeInfo(w, r)

	default:
		s.reverseProxy(w, r)
	}
	duration := time.Since(start)
	log.Debugf("%s request %s %s from %s served in %s", s.class, r.Method, r.RequestURI, r.RemoteAddr, duration)
}

func (s *Handler) reverseProxy(w http.ResponseWriter, r *http.Request) {
	// Check if API is banned
	banDetector := service.GetBanDetector()
	if banDetector.IsBanned(s.class) {
		banned, recoveryTime := banDetector.GetBanStatus(s.class)
		if banned {
			log.Warnf("%s API is banned, returning empty response. Recovery time: %v", s.class, recoveryTime)
			s.returnEmptyResponse(w, r)
			return
		}
	}

	msg := fmt.Sprintf("%s request %s %s from %s is not cachable", s.class, r.Method, r.RequestURI, r.RemoteAddr)
	if s.alwaysShowForwards {
		log.Infof(msg)
	} else {
		log.Tracef(msg)
	}

	service.RateWait(s.ctx, s.class, r.Method, r.URL.Path, r.URL.Query())

	var u *url.URL
	if s.class == service.SPOT {
		r.Host = "api.binance.com"
		u, _ = url.Parse("https://api.binance.com")
	} else {
		r.Host = "fapi.binance.com"
		u, _ = url.Parse("https://fapi.binance.com")
	}

	proxy := httputil.NewSingleHostReverseProxy(u)

	// Create a custom transport to intercept responses
	originalTransport := proxy.Transport
	if originalTransport == nil {
		originalTransport = http.DefaultTransport
	}

	proxy.Transport = &banCheckTransport{
		Transport: originalTransport,
		class:     s.class,
		handler:   s,
		w:         w,
		r:         r,
	}

	proxy.ServeHTTP(w, r)
}

// Response buffer pool for empty responses
var responseBufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
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
}

func (t *banCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)

	// Check for bans
	banDetector := service.GetBanDetector()
	if banDetector.CheckResponse(t.class, resp, err) {
		// API is now banned, return empty response instead
		if resp != nil {
			resp.Body.Close()
		}
		t.handler.returnEmptyResponse(t.w, t.r)
		return nil, nil // Don't return the actual error response
	}

	return resp, err
}
