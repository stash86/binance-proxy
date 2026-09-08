package handler

import (
	"binance-proxy/internal/service"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKlineStatusRouteReportsNoSubscriptions(t *testing.T) {
	for _, class := range []service.Class{service.SPOT, service.FUTURES} {
		t.Run(string(class), func(t *testing.T) {
			// An absent cache must not create a service or contact Binance.
			handler := &Handler{ctx: context.Background(), class: class}
			recorder := httptest.NewRecorder()
			handler.Router(recorder, httptest.NewRequest(http.MethodGet, "/status/klines", nil))
			if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" ||
				recorder.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected response: %d %v", recorder.Code, recorder.Header())
			}
			var status service.KlineStatus
			if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.Class != class || status.Ready || status.Total != 0 || status.Series == nil || len(status.Series) != 0 {
				t.Fatalf("unused proxy reported ready or omitted empty list: %+v", status)
			}
			if handler.srv != nil {
				t.Fatal("status request created a candle service")
			}
		})
	}
}

func TestKlineStatusRouteRejectsOtherMethods(t *testing.T) {
	handler := &Handler{ctx: context.Background(), class: service.SPOT}
	recorder := httptest.NewRecorder()
	handler.Router(recorder, httptest.NewRequest(http.MethodPost, "/status/klines", nil))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("unexpected method response: %d %v", recorder.Code, recorder.Header())
	}
}

func TestKlineStatusRouteReportsShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handler := &Handler{ctx: ctx, class: service.SPOT}
	recorder := httptest.NewRecorder()
	handler.Router(recorder, httptest.NewRequest(http.MethodGet, "/status/klines", nil))
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected shutdown response: %d %v", recorder.Code, recorder.Header())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response["error"] != "service shutting down" {
		t.Fatalf("unexpected shutdown body: %s (error %v)", recorder.Body, err)
	}
}
