package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/time/rate"
)

func TestRequestWeight(t *testing.T) {
	tests := []struct {
		name   string
		class  Class
		method string
		path   string
		query  url.Values
		want   int
	}{
		{name: "spot klines", class: SPOT, method: http.MethodGet, path: "/api/v3/klines", want: 2},
		{name: "futures klines default", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/klines", want: 5},
		{name: "futures klines small", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/klines", query: limitValues("99"), want: 1},
		{name: "futures klines medium", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/klines", query: limitValues("100"), want: 2},
		{name: "futures klines large", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/klines", query: limitValues("500"), want: 5},
		{name: "futures klines largest", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/klines", query: limitValues("1001"), want: 10},
		{name: "spot depth default", class: SPOT, method: http.MethodGet, path: "/api/v3/depth", want: 5},
		{name: "spot depth low", class: SPOT, method: http.MethodGet, path: "/api/v3/depth", query: limitValues("100"), want: 5},
		{name: "spot depth medium", class: SPOT, method: http.MethodGet, path: "/api/v3/depth", query: limitValues("101"), want: 25},
		{name: "spot depth high", class: SPOT, method: http.MethodGet, path: "/api/v3/depth", query: limitValues("1000"), want: 50},
		{name: "spot depth max", class: SPOT, method: http.MethodGet, path: "/api/v3/depth", query: limitValues("5000"), want: 250},
		{name: "futures depth default", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/depth", want: 10},
		{name: "futures depth low", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/depth", query: limitValues("50"), want: 2},
		{name: "futures depth medium", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/depth", query: limitValues("100"), want: 5},
		{name: "futures depth high", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/depth", query: limitValues("500"), want: 10},
		{name: "futures depth max", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/depth", query: limitValues("1000"), want: 20},
		{name: "spot ticker single", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", query: url.Values{"symbol": []string{"BTCUSDT"}}, want: 2},
		{name: "spot ticker all", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", want: 80},
		{name: "spot ticker symbols low", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", query: symbolsValues(20), want: 2},
		{name: "spot ticker symbols medium", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", query: symbolsValues(21), want: 40},
		{name: "spot ticker symbols high", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", query: symbolsValues(101), want: 80},
		{name: "spot ticker invalid symbols", class: SPOT, method: http.MethodGet, path: "/api/v3/ticker/24hr", query: url.Values{"symbols": []string{"not-json"}}, want: 80},
		{name: "futures ticker single", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/ticker/24hr", query: url.Values{"symbol": []string{"BTCUSDT"}}, want: 1},
		{name: "futures ticker all", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/ticker/24hr", want: 40},
		{name: "spot exchange info", class: SPOT, method: http.MethodGet, path: "/api/v3/exchangeInfo", want: 20},
		{name: "futures exchange info", class: FUTURES, method: http.MethodGet, path: "/fapi/v1/exchangeInfo", want: 1},
		{name: "spot get order", class: SPOT, method: http.MethodGet, path: "/api/v3/order", want: 2},
		{name: "spot post order", class: SPOT, method: http.MethodPost, path: "/api/v3/order", want: 1},
		{name: "unknown", class: SPOT, method: http.MethodGet, path: "/api/v3/ping", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RequestWeight(tt.class, tt.method, tt.path, tt.query)
			if got != tt.want {
				t.Fatalf("weight = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRateWaitReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := RateWait(ctx, SPOT, http.MethodGet, "/api/v3/ping", nil); err == nil {
		t.Fatal("expected context error")
	}
}

func TestKlineConnectionAttemptsArePacedAndCancelable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oldSpot, oldFutures := spotKlineConnections, futuresKlineConnections
		spotKlineConnections = rate.NewLimiter(rate.Every(1500*time.Millisecond), 1)
		futuresKlineConnections = rate.NewLimiter(rate.Every(1500*time.Millisecond), 1)
		defer func() { spotKlineConnections, futuresKlineConnections = oldSpot, oldFutures }()
		started := time.Now()
		for _, class := range []Class{SPOT, FUTURES} {
			if err := waitKlineConnection(context.Background(), class); err != nil {
				t.Fatal(err)
			}
		}
		if time.Since(started) != 0 {
			t.Fatal("market types unexpectedly share their initial connection allowance")
		}
		if err := waitKlineConnection(context.Background(), SPOT); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed != 1500*time.Millisecond {
			t.Fatalf("second spot connection after %s, want 1.5s", elapsed)
		}
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() { finished <- waitKlineConnection(ctx, SPOT) }()
		synctest.Wait()
		cancel()
		if err := <-finished; err != context.Canceled {
			t.Fatalf("canceled connection wait = %v", err)
		}
	})
}

func limitValues(limit string) url.Values {
	return url.Values{"limit": []string{limit}}
}

func symbolsValues(count int) url.Values {
	symbols := make([]string, count)
	for i := range symbols {
		symbols[i] = "BTCUSDT"
	}
	encoded, _ := json.Marshal(symbols)
	return url.Values{"symbols": []string{string(encoded)}}
}
