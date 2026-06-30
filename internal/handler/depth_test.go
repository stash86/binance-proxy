package handler

import (
	"binance-proxy/internal/service"
	"net/url"
	"reflect"
	"testing"

	futures "github.com/adshao/go-binance/v2/futures"
)

func TestParseDepthLimit(t *testing.T) {
	tests := []struct {
		name     string
		limit    string
		setLimit bool
		want     int
		ok       bool
	}{
		{name: "default", want: defaultDepthLimit, ok: true},
		{name: "minimum", limit: "5", setLimit: true, want: 5, ok: true},
		{name: "maximum", limit: "20", setLimit: true, want: 20, ok: true},
		{name: "below minimum", limit: "4", setLimit: true, ok: false},
		{name: "above maximum", limit: "21", setLimit: true, ok: false},
		{name: "not integer", limit: "abc", setLimit: true, ok: false},
		{name: "empty value", setLimit: true, want: defaultDepthLimit, ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := url.Values{}
			if tt.setLimit {
				query.Set("limit", tt.limit)
			}

			got, ok := parseDepthLimit(query)
			if ok != tt.ok {
				t.Fatalf("parseDepthLimit() ok = %t, want %t", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("parseDepthLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildDepthResponseCapsBidsAndAsksIndependently(t *testing.T) {
	depth := &service.Depth{
		LastUpdateID: 123,
		Time:         456,
		TradeTime:    789,
		Bids: []futures.Bid{
			{Price: "100.00", Quantity: "1.00"},
			{Price: "99.00", Quantity: "2.00"},
			{Price: "98.00", Quantity: "3.00"},
		},
		Asks: []futures.Ask{
			{Price: "101.00", Quantity: "4.00"},
		},
	}

	got := buildDepthResponse(depth, 2)

	if got.LastUpdateID != depth.LastUpdateID {
		t.Fatalf("LastUpdateID = %d, want %d", got.LastUpdateID, depth.LastUpdateID)
	}
	if got.Time != depth.Time {
		t.Fatalf("Time = %d, want %d", got.Time, depth.Time)
	}
	if got.TradeTime != depth.TradeTime {
		t.Fatalf("TradeTime = %d, want %d", got.TradeTime, depth.TradeTime)
	}

	wantBids := [][2]string{
		{"100.00", "1.00"},
		{"99.00", "2.00"},
	}
	if !reflect.DeepEqual(got.Bids, wantBids) {
		t.Fatalf("Bids = %#v, want %#v", got.Bids, wantBids)
	}

	wantAsks := [][2]string{
		{"101.00", "4.00"},
	}
	if !reflect.DeepEqual(got.Asks, wantAsks) {
		t.Fatalf("Asks = %#v, want %#v", got.Asks, wantAsks)
	}
}

func TestBuildDepthResponseUsesEmptySlices(t *testing.T) {
	got := buildDepthResponse(&service.Depth{}, defaultDepthLimit)

	if got.Bids == nil {
		t.Fatal("Bids is nil, want empty slice")
	}
	if got.Asks == nil {
		t.Fatal("Asks is nil, want empty slice")
	}
}
