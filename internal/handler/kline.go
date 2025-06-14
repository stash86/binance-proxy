package handler

import (
	"binance-proxy/internal/service"
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	// JSON encoder pool for reusing encoders
	encoderPool = sync.Pool{
		New: func() interface{} {
			return json.NewEncoder(nil)
		},
	}

	// Buffer pool for JSON encoding
	jsonBufferPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Buffer{}
		},
	}
)

func (s *Handler) klines(w http.ResponseWriter, r *http.Request) {
	// Check if API is banned
	banDetector := service.GetBanDetector()
	if banDetector.IsBanned(s.class) {
		log.Debugf("%s klines request returning empty due to API ban", s.class)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Data-Source", "ban-protection")
		w.Write([]byte("[]"))
		return
	}

	var fakeKlineTimestampOpen int64 = 0
	symbol := r.URL.Query().Get("symbol")
	interval := r.URL.Query().Get("interval")
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "500"
	}
	limitInt, err := strconv.Atoi(limit)

	switch {
	case err != nil, limitInt <= 0, limitInt > 1000, r.URL.Query().Get("startTime") != "", r.URL.Query().Get("endTime") != "", symbol == "", interval == "":
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

	dataLen := len(data)
	minLen := dataLen
	if minLen > limitInt {
		minLen = limitInt
	}

	// Pre-allocate with exact capacity
	klines := make([]interface{}, 0, minLen)

	// Calculate start index once
	startIdx := dataLen - minLen
	for i := 0; i < minLen; i++ {
		dataIdx := startIdx + i
		klines = append(klines, []interface{}{
			data[dataIdx].OpenTime,
			data[dataIdx].Open,
			data[dataIdx].High,
			data[dataIdx].Low,
			data[dataIdx].Close,
			data[dataIdx].Volume,
			data[dataIdx].CloseTime,
			data[dataIdx].QuoteAssetVolume,
			data[dataIdx].TradeNum,
			data[dataIdx].TakerBuyBaseAssetVolume,
			data[dataIdx].TakerBuyQuoteAssetVolume,
			"0",
		})
	}

	currentTime := time.Now().UnixNano() / 1e6
	if dataLen > 0 && currentTime > data[dataLen-1].CloseTime {
		fakeKlineTimestampOpen = data[dataLen-1].CloseTime + 1
		log.Tracef("%s %s@%s kline requested for %s but not yet received", s.class, symbol, interval, strconv.FormatInt(fakeKlineTimestampOpen, 10))
	}

	if s.enableFakeKline && dataLen > 0 && currentTime > data[dataLen-1].CloseTime {
		log.Tracef("%s %s@%s kline faking candle for timestamp %s", s.class, symbol, interval, strconv.FormatInt(fakeKlineTimestampOpen, 10))
		lastData := data[dataLen-1]
		fakeKline := []interface{}{
			lastData.CloseTime + 1,
			lastData.Close,
			lastData.Close,
			lastData.Close,
			lastData.Close,
			"0.0",
			lastData.CloseTime + 1 + (lastData.CloseTime - lastData.OpenTime),
			"0.0",
			0,
			"0.0",
			"0.0",
			"0",
		}

		if len(klines) >= minLen {
			// Replace last element instead of append + slice
			klines[len(klines)-1] = fakeKline
		} else {
			klines = append(klines, fakeKline)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")

	// Use pooled buffer
	buf := jsonBufferPool.Get().(*bytes.Buffer)
	defer jsonBufferPool.Put(buf)
	buf.Reset()

	// Create encoder with the buffer
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(klines); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	w.Write(buf.Bytes())
}
