package handler

import (
	"binance-proxy/internal/service"
	"net/url"
	"reflect"
	"testing"
)

func TestParseKlineRequest(t *testing.T) {
	tests := []struct {
		name         string
		query        url.Values
		wantSymbol   string
		wantInterval string
		wantLimit    int
		wantOK       bool
	}{
		{
			name:         "default limit",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantLimit:    defaultKlineLimit,
			wantOK:       true,
		},
		{
			name:         "explicit limit",
			query:        url.Values{"symbol": {"ETHUSDT"}, "interval": {"5m"}, "limit": {"250"}},
			wantSymbol:   "ETHUSDT",
			wantInterval: "5m",
			wantLimit:    250,
			wantOK:       true,
		},
		{
			name:         "empty limit uses default",
			query:        url.Values{"symbol": {"ETHUSDT"}, "interval": {"5m"}, "limit": {""}},
			wantSymbol:   "ETHUSDT",
			wantInterval: "5m",
			wantLimit:    defaultKlineLimit,
			wantOK:       true,
		},
		{
			name:         "start time present",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}, "startTime": {""}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantOK:       false,
		},
		{
			name:         "end time present",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}, "endTime": {"123"}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantOK:       false,
		},
		{
			name:         "missing symbol",
			query:        url.Values{"interval": {"1m"}},
			wantInterval: "1m",
			wantOK:       false,
		},
		{
			name:       "missing interval",
			query:      url.Values{"symbol": {"BTCUSDT"}},
			wantSymbol: "BTCUSDT",
			wantOK:     false,
		},
		{
			name:         "invalid limit",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"abc"}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantOK:       false,
		},
		{
			name:         "limit too low",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"0"}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantOK:       false,
		},
		{
			name:         "limit too high",
			query:        url.Values{"symbol": {"BTCUSDT"}, "interval": {"1m"}, "limit": {"1001"}},
			wantSymbol:   "BTCUSDT",
			wantInterval: "1m",
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSymbol, gotInterval, gotLimit, gotOK := parseKlineRequest(tt.query)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %t, want %t", gotOK, tt.wantOK)
			}
			if gotSymbol != tt.wantSymbol {
				t.Fatalf("symbol = %q, want %q", gotSymbol, tt.wantSymbol)
			}
			if gotInterval != tt.wantInterval {
				t.Fatalf("interval = %q, want %q", gotInterval, tt.wantInterval)
			}
			if gotLimit != tt.wantLimit {
				t.Fatalf("limit = %d, want %d", gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestBuildKlineResponseCapsToMostRecentRealKlines(t *testing.T) {
	data := []*service.Kline{
		testKline(1000, 1999, "10"),
		testKline(2000, 2999, "20"),
		testKline(3000, 3999, "30"),
	}

	got := buildKlineResponse(data, 2, 3500, false)

	if got.Stale {
		t.Fatal("Stale = true, want false")
	}
	if got.Fake {
		t.Fatal("Fake = true, want false")
	}
	want := []klineResponseItem{
		klineResponseItem{int64(2000), "20", "20", "20", "20", "1.0", int64(2999), "2.0", int64(10), "0.5", "1.0", "0"},
		klineResponseItem{int64(3000), "30", "30", "30", "30", "1.0", int64(3999), "2.0", int64(10), "0.5", "1.0", "0"},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
}

func TestBuildKlineResponseAppendsFakeBeforeApplyingLimit(t *testing.T) {
	data := []*service.Kline{
		testKline(1000, 1999, "10"),
		testKline(2000, 2999, "20"),
		testKline(3000, 3999, "30"),
	}

	got := buildKlineResponse(data, 3, 5000, true)

	if !got.Stale {
		t.Fatal("Stale = false, want true")
	}
	if !got.Fake {
		t.Fatal("Fake = false, want true")
	}
	if got.FakeOpen != 4000 {
		t.Fatalf("FakeOpen = %d, want 4000", got.FakeOpen)
	}

	want := []klineResponseItem{
		klineResponseItem{int64(2000), "20", "20", "20", "20", "1.0", int64(2999), "2.0", int64(10), "0.5", "1.0", "0"},
		klineResponseItem{int64(3000), "30", "30", "30", "30", "1.0", int64(3999), "2.0", int64(10), "0.5", "1.0", "0"},
		klineResponseItem{int64(4000), "30", "30", "30", "30", "0.0", int64(4999), "0.0", int64(0), "0.0", "0.0", "0"},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
}

func TestBuildKlineResponseDoesNotFakeWhenDisabled(t *testing.T) {
	data := []*service.Kline{
		testKline(1000, 1999, "10"),
	}

	got := buildKlineResponse(data, 1, 5000, false)

	if !got.Stale {
		t.Fatal("Stale = false, want true")
	}
	if got.Fake {
		t.Fatal("Fake = true, want false")
	}
	want := []klineResponseItem{
		klineResponseItem{int64(1000), "10", "10", "10", "10", "1.0", int64(1999), "2.0", int64(10), "0.5", "1.0", "0"},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
}

func TestBuildKlineResponseRechecksSnapshotAtBoundary(t *testing.T) {
	tests := []struct {
		name         string
		nowMS        int64
		final        bool
		needsRefresh bool
	}{
		{name: "unfinished snapshot at close", nowMS: 59999},
		{name: "unfinished snapshot after close", nowMS: 60000, needsRefresh: true},
		{name: "final snapshot within grace", nowMS: 64999, final: true},
		{name: "final snapshot beyond grace", nowMS: 65000, final: true, needsRefresh: true},
	}
	for _, tt := range tests {
		for _, includeFake := range []bool{false, true} {
			name := tt.name
			if includeFake {
				name += " with fake enabled"
			}
			t.Run(name, func(t *testing.T) {
				last := testKline(0, 59999, "10")
				last.IsFinal = tt.final
				got := buildKlineResponse([]*service.Kline{last}, 10, tt.nowMS, includeFake)
				if got.NeedsRefresh != tt.needsRefresh {
					t.Fatalf("NeedsRefresh = %v, want %v", got.NeedsRefresh, tt.needsRefresh)
				}
				if tt.needsRefresh {
					if len(got.Items) != 0 || got.Fake {
						t.Fatalf("unusable snapshot produced items or fake candle: %#v", got)
					}
					return
				}
				wantFake := includeFake && tt.nowMS > last.CloseTime
				wantCount := 1
				if wantFake {
					wantCount++
				}
				if got.Fake != wantFake || len(got.Items) != wantCount {
					t.Fatalf("usable snapshot response = %#v, want fake=%v and %d items", got, wantFake, wantCount)
				}
				if got.Items[0][4] != "10" || last.Close != "10" || last.IsFinal != tt.final {
					t.Fatal("building response changed original candle")
				}
			})
		}
	}
}

func TestBuildKlineResponseSkipsNilKlines(t *testing.T) {
	data := []*service.Kline{
		nil,
		testKline(1000, 1999, "10"),
		nil,
	}

	got := buildKlineResponse(data, 10, 1500, true)

	want := []klineResponseItem{
		klineResponseItem{int64(1000), "10", "10", "10", "10", "1.0", int64(1999), "2.0", int64(10), "0.5", "1.0", "0"},
	}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
}

func TestBuildKlineResponseEmptyResult(t *testing.T) {
	tests := []struct {
		name  string
		data  []*service.Kline
		limit int
	}{
		{name: "empty data", limit: 10},
		{name: "nil data", data: []*service.Kline{nil}, limit: 10},
		{name: "zero limit", data: []*service.Kline{testKline(1000, 1999, "10")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildKlineResponse(tt.data, tt.limit, 5000, true)
			if len(got.Items) != 0 {
				t.Fatalf("items len = %d, want 0", len(got.Items))
			}
			if got.Items == nil {
				t.Fatal("items is nil, want empty slice")
			}
		})
	}
}

func TestFakeKlineHandlesNegativeDuration(t *testing.T) {
	got := fakeKline(testKline(2000, 1000, "10"))

	if got.OpenTime != 1001 {
		t.Fatalf("OpenTime = %d, want 1001", got.OpenTime)
	}
	if got.CloseTime != 1001 {
		t.Fatalf("CloseTime = %d, want 1001", got.CloseTime)
	}
}

func testKline(openTime, closeTime int64, close string) *service.Kline {
	return &service.Kline{
		OpenTime:                 openTime,
		Open:                     close,
		High:                     close,
		Low:                      close,
		Close:                    close,
		Volume:                   "1.0",
		CloseTime:                closeTime,
		QuoteAssetVolume:         "2.0",
		TradeNum:                 10,
		TakerBuyBaseAssetVolume:  "0.5",
		TakerBuyQuoteAssetVolume: "1.0",
		IsFinal:                  true,
	}
}
