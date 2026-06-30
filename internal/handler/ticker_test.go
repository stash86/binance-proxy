package handler

import (
	"binance-proxy/internal/service"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestParseTickerRequest(t *testing.T) {
	tests := []struct {
		name       string
		query      url.Values
		wantSymbol string
		wantOK     bool
	}{
		{
			name:       "symbol only",
			query:      url.Values{"symbol": {"BTCUSDT"}},
			wantSymbol: "BTCUSDT",
			wantOK:     true,
		},
		{
			name:   "missing symbol",
			query:  url.Values{},
			wantOK: false,
		},
		{
			name:   "empty symbol",
			query:  url.Values{"symbol": {""}},
			wantOK: false,
		},
		{
			name:       "multiple symbol values",
			query:      url.Values{"symbol": {"BTCUSDT", "ETHUSDT"}},
			wantSymbol: "BTCUSDT",
			wantOK:     false,
		},
		{
			name:       "symbols parameter",
			query:      url.Values{"symbols": {`["BTCUSDT","ETHUSDT"]`}},
			wantSymbol: "",
			wantOK:     false,
		},
		{
			name:       "extra type parameter",
			query:      url.Values{"symbol": {"BTCUSDT"}, "type": {"MINI"}},
			wantSymbol: "BTCUSDT",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSymbol, gotOK := parseTickerRequest(tt.query)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %t, want %t", gotOK, tt.wantOK)
			}
			if gotSymbol != tt.wantSymbol {
				t.Fatalf("symbol = %q, want %q", gotSymbol, tt.wantSymbol)
			}
		})
	}
}

func TestWriteTickerResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	ticker := &service.Ticker24hr{
		Symbol:    "BTCUSDT",
		LastPrice: "100.00",
		BidPrice:  "99.00",
		AskPrice:  "101.00",
		Count:     42,
	}

	if err := writeTickerResponse(recorder, ticker); err != nil {
		t.Fatalf("writeTickerResponse() error = %v", err)
	}

	result := recorder.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if got := result.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := result.Header.Get("Data-Source"); got != "websocket" {
		t.Fatalf("Data-Source = %q, want websocket", got)
	}

	var got service.Ticker24hr
	if err := json.NewDecoder(result.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Symbol != ticker.Symbol {
		t.Fatalf("Symbol = %q, want %q", got.Symbol, ticker.Symbol)
	}
	if got.LastPrice != ticker.LastPrice {
		t.Fatalf("LastPrice = %q, want %q", got.LastPrice, ticker.LastPrice)
	}
	if got.Count != ticker.Count {
		t.Fatalf("Count = %d, want %d", got.Count, ticker.Count)
	}
}

func TestWriteTickerResponseReturnsWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	writer := errResponseWriter{
		header: http.Header{},
		err:    wantErr,
	}

	err := writeTickerResponse(writer, &service.Ticker24hr{Symbol: "BTCUSDT"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("writeTickerResponse() error = %v, want %v", err, wantErr)
	}

	if got := writer.header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := writer.header.Get("Data-Source"); got != "websocket" {
		t.Fatalf("Data-Source = %q, want websocket", got)
	}
}
