package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	spot "github.com/adshao/go-binance/v2"
	"golang.org/x/time/rate"
)

// The handler package's equivalent adapter is private and importing the handler
// here would create an import cycle. Keep this HTTP fixture in service tests.
type klineTestTransport func(*http.Request) (*http.Response, error)

func (f klineTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func useKlineTestREST(t *testing.T, transport klineTestTransport) {
	t.Helper()
	oldClient, oldBan := http.DefaultClient, globalBanDetector
	oldSpot, oldFutures := SpotLimiter, FuturesLimiter
	http.DefaultClient = &http.Client{Transport: transport}
	globalBanDetector = &BanDetector{}
	SpotLimiter = rate.NewLimiter(rate.Inf, 1200)
	FuturesLimiter = rate.NewLimiter(rate.Inf, 2400)
	t.Cleanup(func() {
		http.DefaultClient, globalBanDetector = oldClient, oldBan
		SpotLimiter, FuturesLimiter = oldSpot, oldFutures
	})
}

func klineRESTResponse(t *testing.T, req *http.Request, candles ...*Kline) *http.Response {
	t.Helper()
	rows := make([][]interface{}, 0, len(candles))
	for _, k := range candles {
		rows = append(rows, []interface{}{k.OpenTime, k.Open, k.High, k.Low, k.Close, k.Volume,
			k.CloseTime, k.QuoteAssetVolume, k.TradeNum, k.TakerBuyBaseAssetVolume, k.TakerBuyQuoteAssetVolume, "0"})
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(body))), Request: req}
}

func TestKlineBootstrapWaitsForNewerEvent(t *testing.T) {
	for _, class := range []Class{SPOT, FUTURES} {
		t.Run(string(class), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := NewKlinesSrv(context.Background(), newSymbolInterval(class, "BTCUSDT", "1m"))
				defer srv.Stop()
				open := time.Now().Truncate(time.Minute).UnixMilli()
				oldEvent := newFuturesKlineEvent(open, "100.00")
				calls := 0
				useKlineTestREST(t, func(req *http.Request) (*http.Response, error) {
					calls++
					wantPath := "/api/v3/klines"
					if class == FUTURES {
						wantPath = "/fapi/v1/klines"
					}
					if req.URL.Path != wantPath || req.URL.Query().Get("limit") != "1000" {
						t.Fatalf("unexpected bootstrap request: %s", req.URL)
					}
					time.Sleep(time.Second)
					k, _, _ := klineFromWSEvent(newFuturesKlineEvent(open, "105.00"))
					return klineRESTResponse(t, req, k), nil
				})
				if class == SPOT {
					srv.wsHandler(srv.ctx, &spot.WsKlineEvent{Time: oldEvent.Time, Kline: spot.WsKline{
						StartTime: open, EndTime: open + 59999, Open: "100.00", Close: "100.00",
					}})
				} else {
					srv.wsHandler(srv.ctx, oldEvent)
				}
				if calls != 1 || len(srv.klinesArr) != 1 || srv.klinesArr[0].Close != "105.00" {
					t.Fatal("triggering event overwrote the REST snapshot or bootstrap was skipped")
				}
				if got := srv.GetKlines(); got != nil {
					t.Fatal("bootstrap alone exposed websocket cache")
				}
				srv.wsHandler(srv.ctx, oldEvent)
				if !srv.lastUpdate.IsZero() || srv.klinesArr[0].Close != "105.00" {
					t.Fatal("buffered event renewed freshness or regressed REST history")
				}
				srv.wsHandler(srv.ctx, newFuturesKlineEvent(open, "106.00"))
				if got := srv.GetKlines(); len(got) != 1 || got[0].Close != "106.00" {
					t.Fatalf("fresh event did not restore cache: %#v", got)
				}
			})
		})
	}
}

func TestKlineCanceledBootstrapCannotPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
		defer srv.Stop()
		connectionCtx, cancel := context.WithCancel(srv.ctx)
		defer cancel()
		entered := make(chan struct{})
		finished := make(chan struct{})
		useKlineTestREST(t, func(req *http.Request) (*http.Response, error) {
			close(entered)
			<-req.Context().Done()
			// Even a transport completing successfully after cancellation must
			// not repopulate the old connection's cache.
			k, _, _ := klineFromWSEvent(newFuturesKlineEvent(time.Now().UnixMilli(), "200.00"))
			return klineRESTResponse(t, req, k), nil
		})
		go func() {
			srv.initKlineData(connectionCtx)
			close(finished)
		}()
		<-entered
		cancel()
		<-finished
		if srv.klinesList != nil || srv.klinesArr != nil || !srv.historyAt.IsZero() {
			t.Fatal("canceled bootstrap published history")
		}
	})
}

func TestKlineRepairsGapAndMissedFinalUpdate(t *testing.T) {
	for _, missedWholeCandle := range []bool{false, true} {
		t.Run(map[bool]string{false: "missed final update", true: "missing candle"}[missedWholeCandle], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := NewKlinesSrv(context.Background(), newSymbolInterval(FUTURES, "BTCUSDT", "1m"))
				defer srv.Stop()
				open := time.Now().Truncate(time.Minute).UnixMilli()
				previousOpen := open - 60000
				if missedWholeCandle {
					previousOpen -= 60000
				}
				old, _, _ := klineFromWSEvent(newFuturesKlineEvent(previousOpen, "90.00"))
				old.IsFinal = missedWholeCandle
				seedKlineHistory(srv, old)
				srv.initDone()
				calls := 0
				useKlineTestREST(t, func(req *http.Request) (*http.Response, error) {
					calls++
					if got := srv.GetKlines(); got != nil {
						t.Fatal("cache remained available during gap repair")
					}
					previous, _, _ := klineFromWSEvent(newFuturesKlineEvent(open-60000, "99.00"))
					current, _, _ := klineFromWSEvent(newFuturesKlineEvent(open, "101.00"))
					return klineRESTResponse(t, req, previous, current), nil
				})
				srv.wsHandler(srv.ctx, newFuturesKlineEvent(open, "100.00"))
				if calls != 1 || len(srv.klinesArr) != 2 || srv.klinesArr[0].Close != "99.00" {
					t.Fatal("missing/final candle was not repaired from REST")
				}
				if got := srv.GetKlines(); got != nil {
					t.Fatal("repair published before receiving a newer event")
				}
				time.Sleep(time.Millisecond)
				srv.wsHandler(srv.ctx, newFuturesKlineEvent(open, "102.00"))
				got := srv.GetKlines()
				if len(got) != 2 || got[0].Close != "99.00" || got[1].Close != "102.00" {
					t.Fatalf("unexpected recovered history: %#v", got)
				}
			})
		})
	}
}
