package service

import (
	"container/list"
	"context"
	"testing"
	"testing/synctest"
	"time"

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
	defer srv.Stop()
	now := time.Now()
	seedKlineHistory(srv, &Kline{
		OpenTime: now.UnixMilli(), CloseTime: now.Add(time.Minute).UnixMilli() - 1,
		Open: "100.00", Close: "101.00",
	})
	srv.lastUpdate = now
	srv.lastEvent = now.UnixMilli()
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

	srv.wsHandler(srv.ctx, nil)
	srv.wsHandler(srv.ctx, "unknown")

	srv.Stop()
	if got := srv.GetKlines(); got != nil {
		t.Fatalf("klines = %#v, want nil", got)
	}
}

func TestKlinesHandlerMergesWebsocketEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		seedKlineHistory(srv)
		openTime := time.Now().Add(-59 * time.Second).UnixMilli()

		srv.wsHandler(srv.ctx, newFuturesKlineEvent(openTime, "100.00"))
		got := srv.GetKlines()
		if len(got) != 1 {
			t.Fatalf("len(klines) = %d, want 1", len(got))
		}
		if got[0].Close != "100.00" {
			t.Fatalf("close = %q, want 100.00", got[0].Close)
		}

		time.Sleep(time.Millisecond)
		srv.wsHandler(srv.ctx, newFuturesKlineEvent(openTime, "101.00"))
		got = srv.GetKlines()
		if len(got) != 1 {
			t.Fatalf("len(klines) after replace = %d, want 1", len(got))
		}
		if got[0].Close != "101.00" {
			t.Fatalf("close after replace = %q, want 101.00", got[0].Close)
		}

		time.Sleep(998 * time.Millisecond)
		final := newFuturesKlineEvent(openTime, "101.00")
		final.Kline.IsFinal = true
		srv.wsHandler(srv.ctx, final)
		time.Sleep(time.Millisecond)
		nextOpenTime := openTime + time.Minute.Milliseconds()
		srv.wsHandler(srv.ctx, newFuturesKlineEvent(nextOpenTime, "102.00"))
		got = srv.GetKlines()
		if len(got) != 2 {
			t.Fatalf("len(klines) after append = %d, want 2", len(got))
		}
		if got[1].OpenTime != nextOpenTime {
			t.Fatalf("second open time = %d, want %d", got[1].OpenTime, nextOpenTime)
		}
		if got[0].Close != "101.00" || !got[0].IsFinal || got[1].Close != "102.00" {
			t.Fatalf("merged candles = %#v, %#v", got[0], got[1])
		}
	})
}

func seedKlineHistory(srv *KlinesSrv, klines ...*Kline) {
	srv.klinesList = list.New()
	for _, k := range klines {
		srv.klinesList.PushBack(k)
	}
	srv.klinesArr = klinesFromList(srv.klinesList)
}

func newFuturesKlineEvent(openTime int64, close string) *futures.WsKlineEvent {
	return &futures.WsKlineEvent{
		Time: time.Now().UnixMilli(),
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
