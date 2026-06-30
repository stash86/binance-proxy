package handler

import (
	"binance-proxy/internal/service"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestGetProxyHTTPClientReusesTransport(t *testing.T) {
	proxyHTTPClient = nil
	proxyHTTPClientOnce = sync.Once{}
	defer func() {
		proxyHTTPClient = nil
		proxyHTTPClientOnce = sync.Once{}
	}()

	first := getProxyHTTPClient()
	second := getProxyHTTPClient()

	if first == nil {
		t.Fatal("first client is nil")
	}
	if second == nil {
		t.Fatal("second client is nil")
	}
	if first != second {
		t.Fatal("getProxyHTTPClient returned different clients, want shared client")
	}
	if first.Transport == nil {
		t.Fatal("client transport is nil")
	}
	if first.Transport != second.Transport {
		t.Fatal("client transport was not reused")
	}
}

func TestProxyTargetURL(t *testing.T) {
	tests := []struct {
		name       string
		class      service.Class
		wantURL    string
		wantHost   string
		wantScheme string
	}{
		{name: "spot", class: service.SPOT, wantURL: spotProxyBaseURL, wantHost: "api.binance.com", wantScheme: "https"},
		{name: "futures", class: service.FUTURES, wantURL: futuresProxyBaseURL, wantHost: "fapi.binance.com", wantScheme: "https"},
		{name: "unknown defaults to futures", class: service.Class("UNKNOWN"), wantURL: futuresProxyBaseURL, wantHost: "fapi.binance.com", wantScheme: "https"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := proxyTargetURL(tt.class)
			if err != nil {
				t.Fatalf("proxyTargetURL() error = %v", err)
			}
			if got.String() != tt.wantURL {
				t.Fatalf("proxyTargetURL() = %q, want %q", got.String(), tt.wantURL)
			}
			if got.Host != tt.wantHost {
				t.Fatalf("host = %q, want %q", got.Host, tt.wantHost)
			}
			if got.Scheme != tt.wantScheme {
				t.Fatalf("scheme = %q, want %q", got.Scheme, tt.wantScheme)
			}
		})
	}
}

func TestSyntheticEmptyResponseBody(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "spot klines", path: "/api/v3/klines", want: "[]"},
		{name: "futures klines", path: "/fapi/v1/klines", want: "[]"},
		{name: "spot depth", path: "/api/v3/depth", want: `{"lastUpdateId":0,"bids":[],"asks":[]}`},
		{name: "futures depth", path: "/fapi/v1/depth", want: `{"lastUpdateId":0,"bids":[],"asks":[]}`},
		{name: "ticker", path: "/api/v3/ticker/24hr", want: "{}"},
		{name: "unknown", path: "/api/v3/exchangeInfo", want: "{}"},
		{name: "empty", want: "{}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syntheticEmptyResponseBody(tt.path)
			if string(got) != tt.want {
				t.Fatalf("syntheticEmptyResponseBody() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestRequestAndResponsePath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v3/depth?symbol=BTCUSDT", nil)
	if got := requestPath(req); got != "/api/v3/depth" {
		t.Fatalf("requestPath() = %q, want /api/v3/depth", got)
	}
	if got := requestPath(nil); got != "" {
		t.Fatalf("requestPath(nil) = %q, want empty", got)
	}
	if got := responsePath(&http.Response{Request: req}); got != "/api/v3/depth" {
		t.Fatalf("responsePath() = %q, want /api/v3/depth", got)
	}
	if got := responsePath(nil); got != "" {
		t.Fatalf("responsePath(nil) = %q, want empty", got)
	}
}

func TestReturnEmptyResponse(t *testing.T) {
	handler := &Handler{class: service.SPOT}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v3/depth", nil)

	handler.returnEmptyResponse(recorder, req)

	result := recorder.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusTooManyRequests)
	}
	if got := result.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := result.Header.Get("Data-Source"); got != "ban-protection" {
		t.Fatalf("Data-Source = %q, want ban-protection", got)
	}
	if got := result.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := result.Header.Get("X-Proxy-Empty"); got != "1" {
		t.Fatalf("X-Proxy-Empty = %q, want 1", got)
	}

	wantBody := `{"lastUpdateId":0,"bids":[],"asks":[]}`
	if got := recorder.Body.String(); got != wantBody {
		t.Fatalf("body = %q, want %q", got, wantBody)
	}
}
