package service

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestKlinesDailySnapshotExpiresWithoutEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1d"))
		defer srv.Stop()
		seedKlineHistory(srv)
		event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
		event.Kline.EndTime = time.Now().Add(24*time.Hour).UnixMilli() - 1
		srv.wsHandler(srv.ctx, event)
		if got := srv.GetKlines(); len(got) != 1 {
			t.Fatalf("fresh daily klines = %#v, want one candle", got)
		}
		time.Sleep(15 * time.Second)
		if got := srv.GetKlines(); len(got) != 1 {
			t.Fatalf("klines at freshness limit = %#v, want one candle", got)
		}
		time.Sleep(time.Millisecond)
		if got := srv.GetKlines(); got != nil {
			t.Fatalf("stalled daily klines = %#v, want nil despite future close time", got)
		}
	})
}

func TestKlinesOldEventDoesNotRenewFreshness(t *testing.T) {
	for _, offset := range []time.Duration{0, -time.Millisecond} {
		t.Run(offset.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
				defer srv.Stop()
				seedKlineHistory(srv)
				event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
				srv.wsHandler(srv.ctx, event)
				time.Sleep(10 * time.Second)
				event.Time += offset.Milliseconds()
				event.Kline.Close = "999.00"
				srv.wsHandler(srv.ctx, event)
				if got := srv.GetKlines(); len(got) != 1 || got[0].Close != "100.00" {
					t.Fatalf("old event changed snapshot: %#v", got)
				}
				time.Sleep(5*time.Second + time.Millisecond)
				if got := srv.GetKlines(); got != nil {
					t.Fatalf("old event renewed freshness: %#v", got)
				}
			})
		})
	}
}

func TestKlinesUnchangedValuesWithNewEventsRemainFresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		seedKlineHistory(srv)
		event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
		srv.wsHandler(srv.ctx, event)
		for i := 0; i < 3; i++ {
			time.Sleep(10 * time.Second)
			event.Time = time.Now().UnixMilli()
			srv.wsHandler(srv.ctx, event)
			if got := srv.GetKlines(); len(got) != 1 || got[0].Close != "100.00" || got[0].Volume != "1.00" {
				t.Fatalf("unchanged candle with advancing event time = %#v, want fresh candle", got)
			}
		}
		time.Sleep(15*time.Second + time.Millisecond)
		if got := srv.GetKlines(); got != nil {
			t.Fatalf("snapshot after updates stopped = %#v, want nil", got)
		}
	})
}

func TestKlinesBoundaryGraceRequiresFinalCandle(t *testing.T) {
	for _, final := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
			defer srv.Stop()
			seedKlineHistory(srv)
			event := newFuturesKlineEvent(time.Now().Add(-time.Minute).UnixMilli(), "100.00")
			event.Kline.IsFinal = final
			srv.wsHandler(srv.ctx, event)
			if got := srv.GetKlines(); (len(got) == 1) != final {
				t.Fatalf("klines at boundary = %#v, available should equal final=%v", got, final)
			}
			time.Sleep(5*time.Second - time.Millisecond)
			if got := srv.GetKlines(); (len(got) == 1) != final {
				t.Fatalf("klines at grace limit = %#v, available should equal final=%v", got, final)
			}
			time.Sleep(time.Millisecond)
			if got := srv.GetKlines(); got != nil {
				t.Fatalf("klines beyond boundary grace = %#v, want nil", got)
			}
		})
	}
}

func TestKlinesStopAndDisconnectClearSnapshot(t *testing.T) {
	for _, action := range []string{"stop", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
				defer srv.Stop()
				seedKlineHistory(srv)
				event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
				srv.wsHandler(srv.ctx, event)
				if got := srv.GetKlines(); len(got) != 1 {
					t.Fatalf("initial snapshot = %#v, want one candle", got)
				}
				if action == "stop" {
					srv.Stop()
					srv.wsHandler(srv.ctx, event)
				} else {
					srv.errHandler(errors.New("use of closed network connection"))
				}
				if got := srv.GetKlines(); got != nil {
					t.Fatalf("snapshot after %s = %#v, want nil", action, got)
				}
				if srv.klinesList != nil || srv.klinesArr != nil || !srv.lastUpdate.IsZero() || srv.lastEvent != 0 || !srv.historyAt.IsZero() {
					t.Fatalf("%s retained cache state", action)
				}
			})
		})
	}
}

func TestKlinesWatchdogCancelsAndJoinsBeforeReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		started := time.Now()
		var attempts []time.Time
		var stoppedAt time.Time
		var canceledBeforeStop bool
		releaseCallback := make(chan struct{})
		runDone := make(chan struct{})
		connect := func(ctx context.Context) (chan struct{}, chan struct{}, error) {
			attempts = append(attempts, time.Now())
			doneC, stopC := make(chan struct{}), make(chan struct{})
			first := len(attempts) == 1
			go func() {
				<-stopC
				if first {
					stoppedAt = time.Now()
					canceledBeforeStop = ctx.Err() != nil
					<-releaseCallback
					// Simulate an old callback completing after shutdown begins.
					// Its canceled context must prevent both cache writes and REST.
					srv.wsHandler(ctx, newFuturesKlineEvent(time.Now().UnixMilli(), "999.00"))
				}
				close(doneC)
			}()
			return doneC, stopC, nil
		}
		go func() {
			defer close(runDone)
			srv.run(connect, func(context.Context) error { return nil })
		}()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if stoppedAt.Sub(started) != 30*time.Second || !canceledBeforeStop {
			t.Fatalf("watchdog stopped after %v, context canceled before stop=%v", stoppedAt.Sub(started), canceledBeforeStop)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if len(attempts) != 1 {
			t.Fatalf("started %d connections while old callback was blocked", len(attempts))
		}
		releasedAt := time.Now()
		close(releaseCallback)
		synctest.Wait()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if len(attempts) != 2 {
			t.Fatalf("connection attempts after callback completed = %d, want 2", len(attempts))
		}
		if delay := attempts[1].Sub(releasedAt); delay < time.Second || delay > 2*time.Second {
			t.Fatalf("replacement delay = %v, want 1s..2s", delay)
		}
		if srv.klinesList != nil || srv.klinesArr != nil {
			t.Fatal("old callback repopulated cache after connection cancellation")
		}
		srv.Stop()
		<-runDone
	})
}

func TestKlinesFlappingConnectionsRetainBackoff(t *testing.T) {
	for _, handshakeFails := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
			defer srv.Stop()
			var attempts []time.Time
			var contexts []context.Context
			var stops []chan struct{}
			runDone := make(chan struct{})
			connect := func(ctx context.Context) (chan struct{}, chan struct{}, error) {
				attempts = append(attempts, time.Now())
				contexts = append(contexts, ctx)
				if len(attempts) == 9 {
					srv.cancel()
				}
				if handshakeFails {
					return nil, nil, errors.New("test handshake failed")
				}
				doneC, stopC := make(chan struct{}), make(chan struct{})
				stops = append(stops, stopC)
				close(doneC)
				return doneC, stopC, nil
			}
			go func() {
				defer close(runDone)
				srv.run(connect, func(context.Context) error { return nil })
			}()
			<-runDone
			bases := []time.Duration{2, 4, 8, 16, 32, 60, 60, 60}
			if len(attempts) != len(bases)+1 {
				t.Fatalf("attempt count = %d, want %d", len(attempts), len(bases)+1)
			}
			for i, seconds := range bases {
				base := seconds * time.Second
				if got := attempts[i+1].Sub(attempts[i]); got < base/2 || got > base {
					t.Fatalf("retry %d delay = %v, want %v..%v (handshake fails=%v)", i+1, got, base/2, base, handshakeFails)
				}
			}
			for i, ctx := range contexts {
				if ctx.Err() == nil {
					t.Fatalf("connection %d context was not canceled", i+1)
				}
			}
			for i, stopC := range stops {
				select {
				case <-stopC:
				default:
					t.Fatalf("connection %d stop channel was not closed", i+1)
				}
			}
		})
	}
}

func TestKlinesSustainedHealthyDeliveryResetsBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1d"))
		defer srv.Stop()
		var attempts []time.Time
		var disconnectedAt time.Time
		runDone := make(chan struct{})
		connect := func(ctx context.Context) (chan struct{}, chan struct{}, error) {
			attempts = append(attempts, time.Now())
			if len(attempts) != 3 {
				if len(attempts) == 4 {
					srv.cancel()
				}
				return nil, nil, errors.New("test handshake failed")
			}
			seedKlineHistory(srv)
			event := newFuturesKlineEvent(time.Now().UnixMilli(), "100.00")
			event.Kline.EndTime = time.Now().Add(24*time.Hour).UnixMilli() - 1
			srv.wsHandler(ctx, event)
			doneC, stopC := make(chan struct{}), make(chan struct{})
			go func() {
				// The first watchdog observation occurs after 1s. Deliver for
				// 62s so it observes at least a full minute of healthy data.
				for i := 0; i < 62; i++ {
					time.Sleep(time.Second)
					event.Time = time.Now().UnixMilli()
					srv.wsHandler(ctx, event)
				}
				disconnectedAt = time.Now()
				close(doneC)
			}()
			return doneC, stopC, nil
		}
		go func() {
			defer close(runDone)
			srv.run(connect, func(context.Context) error { return nil })
		}()
		<-runDone
		if len(attempts) != 4 {
			t.Fatalf("attempt count = %d, want 4", len(attempts))
		}
		if got := attempts[2].Sub(attempts[1]); got < 2*time.Second || got > 4*time.Second {
			t.Fatalf("delay before healthy connection = %v, want advanced backoff 2s..4s", got)
		}
		if got := attempts[3].Sub(disconnectedAt); got < time.Second || got > 2*time.Second {
			t.Fatalf("delay after healthy connection = %v, want reset backoff 1s..2s", got)
		}
	})
}

func TestKlinesWatchdogAllowsRecoveryOnLongLivedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		seedKlineHistory(srv)
		srv.wsHandler(srv.ctx, newFuturesKlineEvent(time.Now().UnixMilli(), "100.00"))
		started := time.Now().Add(-2 * time.Minute)
		watchDone := make(chan time.Time, 1)
		go func() {
			srv.watchConnection(make(chan struct{}), started)
			watchDone <- time.Now()
		}()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		srv.clearKlineList()
		recoveryStarted := time.Now()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		// Repeated errors while history is already unavailable must not keep
		// moving the recovery deadline and prevent a necessary reconnect.
		srv.clearKlineList()
		time.Sleep(19 * time.Second)
		synctest.Wait()
		select {
		case stopped := <-watchDone:
			t.Fatalf("watchdog ended after %v of recovery, want full 30s allowance", stopped.Sub(recoveryStarted))
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case stopped := <-watchDone:
			if got := stopped.Sub(recoveryStarted); got != 30*time.Second {
				t.Fatalf("recovery allowance = %v, want 30s", got)
			}
		default:
			t.Fatal("watchdog did not reconnect after 30s of unavailable history")
		}
	})
}

func TestKlinesWatchdogAllowanceStartsAfterHandshake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		var connectedAt time.Time
		stopped := make(chan time.Time, 1)
		runDone := make(chan struct{})
		connect := func(ctx context.Context) (chan struct{}, chan struct{}, error) {
			time.Sleep(45 * time.Second)
			connectedAt = time.Now()
			doneC, stopC := make(chan struct{}), make(chan struct{})
			go func() {
				<-stopC
				stopped <- time.Now()
				close(doneC)
			}()
			return doneC, stopC, nil
		}
		go func() {
			defer close(runDone)
			srv.run(connect, func(context.Context) error { return nil })
		}()
		synctest.Wait()
		time.Sleep(45 * time.Second)
		synctest.Wait()
		if connectedAt.IsZero() {
			t.Fatal("fake handshake did not complete")
		}
		time.Sleep(29 * time.Second)
		synctest.Wait()
		select {
		case stoppedAt := <-stopped:
			t.Fatalf("watchdog stopped %v after handshake, want full 30s allowance", stoppedAt.Sub(connectedAt))
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case stoppedAt := <-stopped:
			if got := stoppedAt.Sub(connectedAt); got != 30*time.Second {
				t.Fatalf("post-handshake allowance = %v, want 30s", got)
			}
		default:
			t.Fatal("watchdog did not stop stalled socket 30s after handshake")
		}
		srv.Stop()
		<-runDone
	})
}
