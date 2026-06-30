package service

import (
	"container/list"
	"context"
	"testing"

	futures "github.com/adshao/go-binance/v2/futures"
)

func TestKlinesGetReturnsNilWhenStoppedBeforeInit(t *testing.T) {
	srv := NewKlinesSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", "1m"))
	srv.Stop()

	if got := srv.GetKlines(); got != nil {
		t.Fatalf("klines = %#v, want nil", got)
	}
}

func TestKlinesGetReturnsCopy(t *testing.T) {
	srv := NewKlinesSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", "1m"))
	srv.klinesArr = []*Kline{
		{OpenTime: 1, Open: "100.00", Close: "101.00"},
	}
	srv.initDone()

	got := srv.GetKlines()
	if len(got) != 1 {
		t.Fatalf("len(klines) = %d, want 1", len(got))
	}
	got[0].Open = "mutated"

	got = srv.GetKlines()
	if got[0].Open != "100.00" {
		t.Fatalf("open = %q, want original value", got[0].Open)
	}
}

func TestKlinesHandlerIgnoresInvalidEvents(t *testing.T) {
	srv := NewKlinesSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", "1m"))

	srv.wsHandler(nil)
	srv.wsHandler("unknown")

	srv.Stop()
	if got := srv.GetKlines(); got != nil {
		t.Fatalf("klines = %#v, want nil", got)
	}
}

func TestKlinesHandlerMergesWebsocketEvents(t *testing.T) {
	srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
	srv.setKlineList(list.New(), true)
	srv.initDone()

	srv.wsHandler(newFuturesKlineEvent(1000, "100.00"))
	got := srv.GetKlines()
	if len(got) != 1 {
		t.Fatalf("len(klines) = %d, want 1", len(got))
	}
	if got[0].Close != "100.00" {
		t.Fatalf("close = %q, want 100.00", got[0].Close)
	}

	srv.wsHandler(newFuturesKlineEvent(1000, "101.00"))
	got = srv.GetKlines()
	if len(got) != 1 {
		t.Fatalf("len(klines) after replace = %d, want 1", len(got))
	}
	if got[0].Close != "101.00" {
		t.Fatalf("close after replace = %q, want 101.00", got[0].Close)
	}

	srv.wsHandler(newFuturesKlineEvent(2000, "102.00"))
	got = srv.GetKlines()
	if len(got) != 2 {
		t.Fatalf("len(klines) after append = %d, want 2", len(got))
	}
	if got[1].OpenTime != 2000 {
		t.Fatalf("second open time = %d, want 2000", got[1].OpenTime)
	}
}

func newFuturesKlineEvent(openTime int64, close string) *futures.WsKlineEvent {
	return &futures.WsKlineEvent{
		Kline: futures.WsKline{
			StartTime:            openTime,
			EndTime:              openTime + 59999,
			Open:                 close,
			High:                 close,
			Low:                  close,
			Close:                close,
			Volume:               "1.00",
			QuoteVolume:          "1.00",
			TradeNum:             1,
			ActiveBuyVolume:      "0.50",
			ActiveBuyQuoteVolume: "0.50",
		},
	}
}
