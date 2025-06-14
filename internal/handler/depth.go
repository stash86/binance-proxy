package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
)

// Reuse the pools from kline.go
var depthEncoderPool = sync.Pool{
	New: func() interface{} {
		return json.NewEncoder(nil)
	},
}

var depthBufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

func (s *Handler) depth(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	limitInt, err := strconv.Atoi(limit)
	switch {
	case err != nil, symbol == "", limitInt < 5, limitInt > 20:
		s.reverseProxy(w, r)
		return
	}

	depth := s.srv.Depth(symbol)
	if depth == nil {
		s.reverseProxy(w, r)
		return
	}

	bidsLen := len(depth.Bids)
	asksLen := len(depth.Asks)
	minLen := bidsLen
	if asksLen < minLen {
		minLen = asksLen
	}
	if minLen > limitInt {
		minLen = limitInt
	}

	// Pre-allocate with exact capacity
	bids := make([][2]string, minLen)
	asks := make([][2]string, minLen)

	for i := 0; i < minLen; i++ {
		asks[i] = [2]string{
			depth.Asks[i].Price,
			depth.Asks[i].Quantity,
		}
		bids[i] = [2]string{
			depth.Bids[i].Price,
			depth.Bids[i].Quantity,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")

	// Use pooled buffer
	buf := depthBufferPool.Get().(*bytes.Buffer)
	defer depthBufferPool.Put(buf)
	buf.Reset()

	// Create encoder with the buffer
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)

	response := map[string]interface{}{
		"lastUpdateId": depth.LastUpdateID,
		"E":            depth.Time,
		"T":            depth.TradeTime,
		"bids":         bids,
		"asks":         asks,
	}

	if err := encoder.Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	w.Write(buf.Bytes())
}
