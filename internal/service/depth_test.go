package service

import (
	"context"
	"testing"

	futures "github.com/adshao/go-binance/v2/futures"
)

func TestDepthGetDepthReturnsNilWhenStoppedBeforeInit(t *testing.T) {
	srv := NewDepthSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))
	srv.Stop()

	if got := srv.GetDepth(); got != nil {
		t.Fatalf("depth = %#v, want nil", got)
	}
}

func TestDepthGetDepthReturnsCopy(t *testing.T) {
	srv := NewDepthSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", ""))
	srv.wsHandlerFutures(&futures.WsDepthEvent{
		LastUpdateID:    123,
		Time:            456,
		TransactionTime: 789,
		Bids: []futures.Bid{
			{Price: "100.00", Quantity: "1.25"},
		},
		Asks: []futures.Ask{
			{Price: "101.00", Quantity: "2.50"},
		},
	})

	got := srv.GetDepth()
	if got == nil {
		t.Fatal("depth = nil, want value")
	}
	got.Bids[0].Price = "mutated"
	got.Asks[0].Quantity = "mutated"

	got = srv.GetDepth()
	if got.Bids[0].Price != "100.00" {
		t.Fatalf("bid price = %q, want original value", got.Bids[0].Price)
	}
	if got.Asks[0].Quantity != "2.50" {
		t.Fatalf("ask quantity = %q, want original value", got.Asks[0].Quantity)
	}
}

func TestDepthHandlersIgnoreNilEvents(t *testing.T) {
	srv := NewDepthSrv(context.Background(), newSymbolInterval(SPOT, "BTCUSDT", ""))

	srv.wsHandler(nil)
	srv.wsHandlerFutures(nil)

	srv.Stop()
	if got := srv.GetDepth(); got != nil {
		t.Fatalf("depth = %#v, want nil", got)
	}
}
