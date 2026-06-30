package handler

import (
	"binance-proxy/internal/service"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

const (
	defaultDepthLimit = 20
	minDepthLimit     = 5
	maxDepthLimit     = 20
)

type depthResponse struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	Time         int64       `json:"E"`
	TradeTime    int64       `json:"T"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
}

func (s *Handler) depth(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	limit, ok := parseDepthLimit(r.URL.Query())
	if !ok || symbol == "" {
		s.reverseProxy(w, r)
		return
	}

	depth := s.srv.Depth(symbol)
	if depth == nil {
		s.reverseProxy(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")

	// Use shared buffer pool
	buf := GetBuffer()
	defer PutBuffer(buf)

	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(buildDepthResponse(depth, limit)); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	w.Write(buf.Bytes())
}

func parseDepthLimit(query url.Values) (int, bool) {
	limit := query.Get("limit")
	if limit == "" {
		return defaultDepthLimit, true
	}

	limitInt, err := strconv.Atoi(limit)
	if err != nil || limitInt < minDepthLimit || limitInt > maxDepthLimit {
		return 0, false
	}

	return limitInt, true
}

func buildDepthResponse(depth *service.Depth, limit int) depthResponse {
	bidsLen := depthResponseLen(len(depth.Bids), limit)
	asksLen := depthResponseLen(len(depth.Asks), limit)

	bids := make([][2]string, bidsLen)
	for i := 0; i < bidsLen; i++ {
		bids[i] = [2]string{
			depth.Bids[i].Price,
			depth.Bids[i].Quantity,
		}
	}

	asks := make([][2]string, asksLen)
	for i := 0; i < asksLen; i++ {
		asks[i] = [2]string{
			depth.Asks[i].Price,
			depth.Asks[i].Quantity,
		}
	}

	return depthResponse{
		LastUpdateID: depth.LastUpdateID,
		Time:         depth.Time,
		TradeTime:    depth.TradeTime,
		Bids:         bids,
		Asks:         asks,
	}
}

func depthResponseLen(available, limit int) int {
	if available < limit {
		return available
	}

	return limit
}
