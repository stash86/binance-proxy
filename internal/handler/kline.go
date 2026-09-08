package handler

import (
	"binance-proxy/internal/service"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	defaultKlineLimit = 500
	maxKlineLimit     = 1000
)

type klineResponseItem []interface{}

type klineResponse struct {
	Items        []klineResponseItem
	Stale        bool
	Fake         bool
	FakeOpen     int64
	NeedsRefresh bool
}

func (s *Handler) klines(w http.ResponseWriter, r *http.Request) {
	// Check if API is banned
	banDetector := service.GetBanDetector()
	if banDetector != nil && banDetector.IsBanned(s.class) {
		log.Debugf("%s klines request returning empty due to API ban", s.class)
		s.returnEmptyResponse(w, r)
		return
	}

	symbol, interval, limit, ok := parseKlineRequest(r.URL.Query())
	if !ok {
		log.Tracef("%s %s@%s kline proxying via REST", s.class, symbol, interval)
		s.reverseProxy(w, r)
		return
	}

	data := s.srv.Klines(symbol, interval)
	if data == nil {
		log.Tracef("%s %s@%s kline proxying via REST", s.class, symbol, interval)
		s.reverseProxy(w, r)
		return
	}

	currentTime := time.Now().UnixNano() / 1e6
	response := buildKlineResponse(data, limit, currentTime, s.enableFakeKline)
	if response.NeedsRefresh {
		log.Tracef("%s %s@%s kline snapshot crossed freshness boundary; proxying via REST", s.class, symbol, interval)
		s.reverseProxy(w, r)
		return
	}
	if response.Stale {
		log.Tracef("%s %s@%s kline requested for %s but not yet received", s.class, symbol, interval, strconv.FormatInt(response.FakeOpen, 10))
	}
	if response.Fake {
		log.Tracef("%s %s@%s kline faking candle for timestamp %s", s.class, symbol, interval, strconv.FormatInt(response.FakeOpen, 10))
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")

	// Use shared buffer pool
	buf := GetBuffer()
	defer PutBuffer(buf)

	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(response.Items); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Errorf("%s %s@%s kline response write failed: %v", s.class, symbol, interval, err)
	}
}

func parseKlineRequest(query url.Values) (symbol, interval string, limit int, ok bool) {
	symbol = InternSymbol(query.Get("symbol"))
	interval = InternInterval(query.Get("interval"))
	if symbol == "" || interval == "" || hasQueryParam(query, "startTime") || hasQueryParam(query, "endTime") {
		return symbol, interval, 0, false
	}

	limitValue := query.Get("limit")
	if limitValue == "" {
		return symbol, interval, defaultKlineLimit, true
	}

	limit, err := strconv.Atoi(limitValue)
	if err != nil || limit <= 0 || limit > maxKlineLimit {
		return symbol, interval, 0, false
	}

	return symbol, interval, limit, true
}

func hasQueryParam(query url.Values, key string) bool {
	_, ok := query[key]
	return ok
}

func buildKlineResponse(data []*service.Kline, limit int, nowMS int64, includeFake bool) klineResponse {
	if limit <= 0 {
		return klineResponse{Items: []klineResponseItem{}}
	}

	klines := make([]*service.Kline, 0, len(data)+1)
	for _, kline := range data {
		if kline != nil {
			klines = append(klines, kline)
		}
	}
	if len(klines) == 0 {
		return klineResponse{Items: []klineResponseItem{}}
	}

	lastKline := klines[len(klines)-1]
	result := klineResponse{
		Stale:        nowMS > lastKline.CloseTime,
		FakeOpen:     lastKline.CloseTime + 1,
		NeedsRefresh: !lastKline.IsCurrent(nowMS),
	}
	if result.NeedsRefresh {
		return result
	}
	if includeFake && result.Stale {
		klines = append(klines, fakeKline(lastKline))
		result.Fake = true
	}

	if len(klines) > limit {
		klines = klines[len(klines)-limit:]
	}

	result.Items = make([]klineResponseItem, len(klines))
	for i, kline := range klines {
		result.Items[i] = klineResponseItem{
			kline.OpenTime,
			kline.Open,
			kline.High,
			kline.Low,
			kline.Close,
			kline.Volume,
			kline.CloseTime,
			kline.QuoteAssetVolume,
			kline.TradeNum,
			kline.TakerBuyBaseAssetVolume,
			kline.TakerBuyQuoteAssetVolume,
			"0",
		}
	}

	return result
}

func fakeKline(last *service.Kline) *service.Kline {
	duration := last.CloseTime - last.OpenTime
	if duration < 0 {
		duration = 0
	}

	openTime := last.CloseTime + 1
	return &service.Kline{
		OpenTime:                 openTime,
		Open:                     last.Close,
		High:                     last.Close,
		Low:                      last.Close,
		Close:                    last.Close,
		Volume:                   "0.0",
		CloseTime:                openTime + duration,
		QuoteAssetVolume:         "0.0",
		TradeNum:                 0,
		TakerBuyBaseAssetVolume:  "0.0",
		TakerBuyQuoteAssetVolume: "0.0",
	}
}
