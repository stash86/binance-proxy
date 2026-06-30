package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestServiceAutoRemoveExpiredStopsServices(t *testing.T) {
	svc := newTestService(t)

	klineSI := *newSymbolInterval(SPOT, "BTCUSDT", "1m")
	klineSrv := NewKlinesSrv(svc.ctx, &klineSI)
	svc.klinesSrv.Store(klineSI, klineSrv)
	svc.lastGetKlines.Store(klineSI, time.Now().Add(-3*time.Minute))

	depthSI := *newSymbolInterval(SPOT, "ETHUSDT", "")
	depthSrv := NewDepthSrv(svc.ctx, &depthSI)
	svc.depthSrv.Store(depthSI, depthSrv)
	svc.lastGetDepth.Store(depthSI, time.Now().Add(-3*time.Minute))

	tickerSI := *newSymbolInterval(SPOT, "BNBUSDT", "")
	tickerSrv := NewTickerSrv(svc.ctx, &tickerSI)
	svc.tickerSrv.Store(tickerSI, tickerSrv)
	svc.lastGetTicker.Store(tickerSI, time.Now().Add(-3*time.Minute))

	svc.autoRemoveExpired()

	assertSyncMapMissing(t, &svc.klinesSrv, klineSI)
	assertSyncMapMissing(t, &svc.depthSrv, depthSI)
	assertSyncMapMissing(t, &svc.tickerSrv, tickerSI)
	assertContextCanceled(t, klineSrv.ctx)
	assertContextCanceled(t, depthSrv.ctx)
	assertContextCanceled(t, tickerSrv.ctx)
}

func TestServiceAutoRemoveExpiredSeedsLastAccess(t *testing.T) {
	svc := newTestService(t)

	klineSI := *newSymbolInterval(SPOT, "BTCUSDT", "1m")
	klineSrv := NewKlinesSrv(svc.ctx, &klineSI)
	svc.klinesSrv.Store(klineSI, klineSrv)

	svc.autoRemoveExpired()

	if _, ok := svc.klinesSrv.Load(klineSI); !ok {
		t.Fatal("kline service was removed unexpectedly")
	}
	if _, ok := svc.lastGetKlines.Load(klineSI); !ok {
		t.Fatal("last kline access was not seeded")
	}
}

func TestServiceAutoRemoveExpiredRemovesUnknownKlineInterval(t *testing.T) {
	svc := newTestService(t)

	si := *newSymbolInterval(SPOT, "BTCUSDT", "2m")
	srv := NewKlinesSrv(svc.ctx, &si)
	svc.klinesSrv.Store(si, srv)
	svc.lastGetKlines.Store(si, time.Now())

	svc.autoRemoveExpired()

	assertSyncMapMissing(t, &svc.klinesSrv, si)
	assertContextCanceled(t, srv.ctx)
}

func TestServiceStopCancelsAndClearsOwnedServices(t *testing.T) {
	svc := newTestService(t)

	klineSI := *newSymbolInterval(SPOT, "BTCUSDT", "1m")
	klineSrv := NewKlinesSrv(svc.ctx, &klineSI)
	svc.klinesSrv.Store(klineSI, klineSrv)
	svc.lastGetKlines.Store(klineSI, time.Now())

	depthSI := *newSymbolInterval(SPOT, "ETHUSDT", "")
	depthSrv := NewDepthSrv(svc.ctx, &depthSI)
	svc.depthSrv.Store(depthSI, depthSrv)
	svc.lastGetDepth.Store(depthSI, time.Now())

	tickerSI := *newSymbolInterval(SPOT, "BNBUSDT", "")
	tickerSrv := NewTickerSrv(svc.ctx, &tickerSI)
	svc.tickerSrv.Store(tickerSI, tickerSrv)
	svc.lastGetTicker.Store(tickerSI, time.Now())

	svc.Stop()

	assertContextCanceled(t, svc.ctx)
	assertContextCanceled(t, svc.exchangeInfoSrv.ctx)
	assertContextCanceled(t, klineSrv.ctx)
	assertContextCanceled(t, depthSrv.ctx)
	assertContextCanceled(t, tickerSrv.ctx)
	assertSyncMapMissing(t, &svc.klinesSrv, klineSI)
	assertSyncMapMissing(t, &svc.depthSrv, depthSI)
	assertSyncMapMissing(t, &svc.tickerSrv, tickerSI)
	assertSyncMapMissing(t, &svc.lastGetKlines, klineSI)
	assertSyncMapMissing(t, &svc.lastGetDepth, depthSI)
	assertSyncMapMissing(t, &svc.lastGetTicker, tickerSI)
}

func TestServiceMethodsReturnNilAfterStop(t *testing.T) {
	svc := newTestService(t)
	svc.Stop()

	if got := svc.ExchangeInfo(); got != nil {
		t.Fatalf("exchange info = %q, want nil", string(got))
	}
	if got := svc.Klines("BTCUSDT", "1m"); got != nil {
		t.Fatalf("klines = %#v, want nil", got)
	}
	if got := svc.Depth("BTCUSDT"); got != nil {
		t.Fatalf("depth = %#v, want nil", got)
	}
	if got := svc.Ticker("BTCUSDT"); got != nil {
		t.Fatalf("ticker = %#v, want nil", got)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	svc := &Service{
		class: SPOT,
		ctx:   ctx,
		cancel: func() {
			cancel()
		},
	}
	svc.exchangeInfoSrv = NewExchangeInfoSrv(svc.ctx, newSymbolInterval(svc.class, "", ""))
	t.Cleanup(svc.Stop)
	return svc
}

func assertSyncMapMissing(t *testing.T, m *sync.Map, key interface{}) {
	t.Helper()

	if _, ok := m.Load(key); ok {
		t.Fatalf("expected key %#v to be removed", key)
	}
}

func assertContextCanceled(t *testing.T, ctx context.Context) {
	t.Helper()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}
