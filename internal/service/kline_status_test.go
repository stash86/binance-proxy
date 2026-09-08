package service

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestKlineStatusEmptyAndInitializingReadsDoNotStartWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestService(t)
		got := svc.KlineStatus()
		if got.Class != svc.class || got.Ready || got.Total != 0 || got.Series == nil || len(got.Series) != 0 {
			t.Fatalf("empty status = %+v", got)
		}
		si := *newSymbolInterval(svc.class, "BTCUSDT", "1m")
		srv := NewKlinesSrv(svc.ctx, &si)
		svc.klinesSrv.Store(si, srv)
		started := time.Now()
		got = svc.KlineStatus()
		if time.Since(started) != 0 || got.Ready || got.Total != 1 || got.Initializing != 1 || len(got.Series) != 1 || got.Series[0].State != "initializing" || got.Series[0].Reason != "no_history" {
			t.Fatalf("initial status waited or misclassified series: %+v", got)
		}
		if got.Series[0].UpdateAgeMS != nil || got.Series[0].EventAgeMS != nil {
			t.Fatalf("unseen events have known ages: %+v", got.Series[0])
		}
		assertSyncMapMissing(t, &svc.lastGetKlines, si)
		if srv.klinesList != nil || len(srv.klinesArr) != 0 || !srv.historyAt.IsZero() || !srv.recoveryAt.IsZero() || srv.ctx.Err() != nil {
			t.Fatal("reading status changed the uninitialized series")
		}
		select {
		case <-srv.initCtx.Done():
			t.Fatal("reading status marked the series initialized")
		default:
		}
	})
}

func TestKlineStatusFreshnessExpiresAtSameBoundaryAsCache(t *testing.T) {
	for _, interval := range []string{"1m", "1d"} {
		t.Run(interval, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc := newTestService(t)
				si := *newSymbolInterval(svc.class, "BTCUSDT", interval)
				srv := NewKlinesSrv(svc.ctx, &si)
				svc.klinesSrv.Store(si, srv)
				seedKlineHistory(srv)
				event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
				if interval == "1d" {
					event.Kline.EndTime = time.Now().Add(24*time.Hour).UnixMilli() - 1
				}
				srv.wsHandler(srv.ctx, event)
				for _, elapsed := range []time.Duration{0, 15 * time.Second, time.Millisecond} {
					time.Sleep(elapsed)
					got := svc.KlineStatus()
					age := time.Now().UnixMilli() - event.Time
					wantFresh := age <= 15000
					if got.Ready != wantFresh || (got.Fresh == 1) != wantFresh || (got.Stale == 1) == wantFresh || got.Total != 1 {
						t.Fatalf("status at %dms = %+v", age, got)
					}
					entry := got.Series[0]
					wantReason := "fresh"
					if !wantFresh {
						wantReason = "update_stale"
					}
					if entry.Reason != wantReason {
						t.Fatalf("reason at %dms = %q, want %q", age, entry.Reason, wantReason)
					}
					if entry.UpdateAgeMS == nil || entry.EventAgeMS == nil || *entry.UpdateAgeMS != age || *entry.EventAgeMS != age {
						t.Fatalf("ages at %dms = %+v", age, entry)
					}
					if (srv.GetKlines() != nil) != wantFresh {
						t.Fatal("status disagreed with cache availability")
					}
				}
			})
		})
	}
}

func TestKlineStatusRespectsClosedCandleFinality(t *testing.T) {
	for _, final := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			svc := newTestService(t)
			si := *newSymbolInterval(svc.class, "BTCUSDT", "1m")
			srv := NewKlinesSrv(svc.ctx, &si)
			svc.klinesSrv.Store(si, srv)
			seedKlineHistory(srv)
			event := newFuturesKlineEvent(time.Now().Add(-time.Minute).UnixMilli(), "100.00")
			event.Kline.IsFinal = final
			srv.wsHandler(srv.ctx, event)
			if got := svc.KlineStatus(); got.Ready != final || (got.Stale == 1) == final {
				t.Fatalf("final=%v boundary status = %+v", final, got)
			}
			time.Sleep(5 * time.Second)
			if got := svc.KlineStatus(); got.Ready || got.Stale != 1 || got.Series[0].State != "stale" || got.Series[0].Reason != "candle_not_current" {
				t.Fatalf("expired boundary status = %+v", got)
			}
		})
	}
}

func TestKlineStatusSeparatesExchangeEventAgeFromReceiptAge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestService(t)
		si := *newSymbolInterval(svc.class, "BTCUSDT", "1m")
		srv := NewKlinesSrv(svc.ctx, &si)
		svc.klinesSrv.Store(si, srv)
		seedKlineHistory(srv)
		event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
		event.Time -= 10000
		srv.wsHandler(srv.ctx, event)
		time.Sleep(6 * time.Second)
		got := svc.KlineStatus()
		entry := got.Series[0]
		if got.Ready || got.Stale != 1 || entry.Reason != "event_stale" || entry.UpdateAgeMS == nil || *entry.UpdateAgeMS != 6000 || entry.EventAgeMS == nil || *entry.EventAgeMS != 16000 {
			t.Fatalf("old exchange event with recent receipt = %+v", got)
		}
	})
}

func TestKlineStatusTracksInitializationRecoveryAndStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestService(t)
		si := *newSymbolInterval(svc.class, "BTCUSDT", "1m")
		srv := NewKlinesSrv(svc.ctx, &si)
		svc.klinesSrv.Store(si, srv)
		open := time.Now().UnixMilli()
		loadHistory := func() {
			seedKlineHistory(srv, &Kline{OpenTime: open, CloseTime: open + 59999, Close: "100.00"})
			srv.historyAt = time.Now()
		}
		check := func(state, reason string) {
			t.Helper()
			got := svc.KlineStatus()
			if got.Total != 1 || got.Ready || got.Series[0].State != state || got.Series[0].Reason != reason {
				t.Fatalf("status = %+v, want %s/%s", got, state, reason)
			}
			if got.Series[0].UpdateAgeMS != nil || got.Series[0].EventAgeMS != nil {
				t.Fatalf("missing accepted event has known ages: %+v", got.Series[0])
			}
			counts := map[string]int{"initializing": got.Initializing, "recovering": got.Recovering, "stale": got.Stale}
			if counts[state] != 1 || got.Initializing+got.Recovering+got.Stale != 1 {
				t.Fatalf("wrong counts for %s: %+v", state, got)
			}
		}
		loadHistory()
		check("initializing", "awaiting_ws_event")
		srv.clearKlineList()
		check("recovering", "rebuilding_history")
		loadHistory()
		check("recovering", "awaiting_ws_event")
		time.Sleep(time.Millisecond)
		srv.wsHandler(srv.ctx, newFuturesKlineEvent(open, "101.00"))
		if got := svc.KlineStatus(); !got.Ready || got.Fresh != 1 {
			t.Fatalf("confirming WS event did not restore freshness: %+v", got)
		}
		srv.Stop()
		check("stale", "stopped")
	})
}

func TestKlineStatusSortedSnapshotsDoNotRefreshIdleTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := newTestService(t)
		keys := []symbolInterval{
			*newSymbolInterval(svc.class, "ETHUSDT", "1m"),
			*newSymbolInterval(svc.class, "BTCUSDT", "5m"),
			*newSymbolInterval(svc.class, "BTCUSDT", "1m"),
		}
		lastAccess := time.Now().Add(-time.Minute)
		for _, si := range keys {
			srv := NewKlinesSrv(svc.ctx, &si)
			seedKlineHistory(srv)
			srv.wsHandler(srv.ctx, newFuturesKlineEvent(time.Now().UnixMilli(), "100.00"))
			svc.klinesSrv.Store(si, srv)
			svc.lastGetKlines.Store(si, lastAccess)
		}
		first := svc.KlineStatus()
		if !first.Ready || first.Total != 3 || first.Fresh != 3 {
			t.Fatalf("fresh aggregate = %+v", first)
		}
		want := [][2]string{{"BTCUSDT", "1m"}, {"BTCUSDT", "5m"}, {"ETHUSDT", "1m"}}
		got := make([][2]string, len(first.Series))
		for i, entry := range first.Series {
			got[i] = [2]string{entry.Symbol, entry.Interval}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("series order = %v, want %v", got, want)
		}
		*first.Series[0].UpdateAgeMS = 999
		*first.Series[0].EventAgeMS = 999
		first.Series[0].Symbol = "mutated"
		second := svc.KlineStatus()
		if second.Series[0].Symbol != "BTCUSDT" || *second.Series[0].UpdateAgeMS != 0 || *second.Series[0].EventAgeMS != 0 || *first.Series[1].EventAgeMS != 0 {
			t.Fatal("returned status fields alias another entry or later snapshot")
		}
		for _, si := range keys {
			if got, ok := svc.lastGetKlines.Load(si); !ok || got != lastAccess {
				t.Fatalf("status refreshed %s@%s idle timer: %v", si.Symbol, si.Interval, got)
			}
		}
	})
}
