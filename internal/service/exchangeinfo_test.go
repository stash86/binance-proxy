package service

import (
	"context"
	"testing"
	"time"
)

func TestExchangeInfoGetReturnsNilWhenStoppedBeforeInit(t *testing.T) {
	srv := NewExchangeInfoSrv(context.Background(), newSymbolInterval(SPOT, "", ""))
	srv.Stop()

	if got := srv.GetExchangeInfo(); got != nil {
		t.Fatalf("exchangeInfo = %q, want nil", string(got))
	}
}

func TestExchangeInfoGetReturnsCopy(t *testing.T) {
	srv := NewExchangeInfoSrv(context.Background(), newSymbolInterval(SPOT, "", ""))

	srv.rw.Lock()
	srv.exchangeInfo = []byte(`{"symbols":[]}`)
	srv.rw.Unlock()
	srv.initDone()

	got := srv.GetExchangeInfo()
	if string(got) != `{"symbols":[]}` {
		t.Fatalf("exchangeInfo = %q, want cached payload", string(got))
	}
	got[0] = '['

	got = srv.GetExchangeInfo()
	if string(got) != `{"symbols":[]}` {
		t.Fatalf("exchangeInfo = %q, want unmodified cached payload", string(got))
	}
}

func TestExchangeInfoStopCancelsContext(t *testing.T) {
	srv := NewExchangeInfoSrv(context.Background(), newSymbolInterval(FUTURES, "", ""))
	srv.Stop()

	select {
	case <-srv.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("service context was not canceled")
	}
}
