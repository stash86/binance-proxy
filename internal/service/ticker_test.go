package service

import (
	"context"
	"testing"

	spot "github.com/adshao/go-binance/v2"
)

func TestTickerGetReturnsNilWhenStoppedBeforeInit(t *testing.T) {
	srv := NewTickerSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))
	srv.Stop()

	if got := srv.GetTicker(); got != nil {
		t.Fatalf("ticker = %#v, want nil", got)
	}
}

func TestTickerGetReturnsCopyWithBookTickerOverride(t *testing.T) {
	srv := NewTickerSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))
	srv.wsHandlerTicker24hr(newMarketStatEvent("100.00", "99.00", "101.00"))
	srv.wsHandlerBookTicker(&spot.WsBookTickerEvent{
		Symbol:       "BTCUSDT",
		BestBidPrice: "100.50",
		BestBidQty:   "1.25",
		BestAskPrice: "100.60",
		BestAskQty:   "1.50",
	})

	got := srv.GetTicker()
	if got == nil {
		t.Fatal("ticker = nil, want value")
	}
	if got.BidPrice != "100.50" {
		t.Fatalf("bid price = %q, want book ticker bid", got.BidPrice)
	}
	if got.AskPrice != "100.60" {
		t.Fatalf("ask price = %q, want book ticker ask", got.AskPrice)
	}
	if got.LastPrice != "100.00" {
		t.Fatalf("last price = %q, want market stat last", got.LastPrice)
	}

	got.LastPrice = "mutated"
	got = srv.GetTicker()
	if got.LastPrice != "100.00" {
		t.Fatalf("last price = %q, want cached value unchanged", got.LastPrice)
	}
}

func TestTickerHandlersIgnoreNilEvents(t *testing.T) {
	srv := NewTickerSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))

	srv.wsHandlerTicker24hr(nil)
	srv.wsHandlerBookTicker(nil)

	srv.Stop()
	if got := srv.GetTicker(); got != nil {
		t.Fatalf("ticker = %#v, want nil", got)
	}
}

func TestTickerGetReturnsMarketStatPricesWithoutBookTicker(t *testing.T) {
	srv := NewTickerSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))
	srv.wsHandlerTicker24hr(newMarketStatEvent("100.00", "99.00", "101.00"))

	got := srv.GetTicker()
	if got == nil {
		t.Fatal("ticker = nil, want value")
	}
	if got.LastPrice != "100.00" {
		t.Fatalf("last price = %q, want market stat last", got.LastPrice)
	}
	if got.BidPrice != "99.00" {
		t.Fatalf("bid price = %q, want market stat bid", got.BidPrice)
	}
	if got.AskPrice != "101.00" {
		t.Fatalf("ask price = %q, want market stat ask", got.AskPrice)
	}
}

func newMarketStatEvent(last, bid, ask string) *spot.WsMarketStatEvent {
	return &spot.WsMarketStatEvent{
		Symbol:             "BTCUSDT",
		PriceChange:        "1.00",
		PriceChangePercent: "1.00",
		WeightedAvgPrice:   last,
		PrevClosePrice:     "99.00",
		LastPrice:          last,
		CloseQty:           "0.10",
		BidPrice:           bid,
		AskPrice:           ask,
		OpenPrice:          "99.00",
		HighPrice:          "102.00",
		LowPrice:           "98.00",
		BaseVolume:         "10.00",
		QuoteVolume:        "1000.00",
		OpenTime:           1000,
		CloseTime:          2000,
		FirstID:            1,
		LastID:             2,
		Count:              3,
	}
}
