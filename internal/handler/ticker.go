package handler

import (
	"binance-proxy/internal/service"
	"encoding/json"
	"net/http"
	"net/url"

	log "github.com/sirupsen/logrus"
)

func (s *Handler) ticker(w http.ResponseWriter, r *http.Request) {
	symbol, ok := parseTickerRequest(r.URL.Query())
	if !ok {
		log.Tracef("%s ticker24hr unsupported cache request proxying via REST", s.class)
		s.reverseProxy(w, r)
		return
	}

	ticker := s.srv.Ticker(symbol)
	if ticker == nil {
		log.Tracef("%s ticker24hr for %s proxying via REST", s.class, symbol)
		s.reverseProxy(w, r)
		return
	} else {
		log.Tracef("%s ticker24hr for %s delivering via websocket cache", s.class, symbol)
	}

	if err := writeTickerResponse(w, ticker); err != nil {
		log.Errorf("%s ticker24hr for %s response write failed: %v", s.class, symbol, err)
	}
}

func parseTickerRequest(query url.Values) (string, bool) {
	symbols, ok := query["symbol"]
	if !ok || len(query) != 1 || len(symbols) != 1 || symbols[0] == "" {
		return InternSymbol(query.Get("symbol")), false
	}

	return InternSymbol(symbols[0]), true
}

func writeTickerResponse(w http.ResponseWriter, ticker *service.Ticker24hr) error {
	buf := GetBuffer()
	defer PutBuffer(buf)

	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(ticker); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")
	_, err := w.Write(buf.Bytes())
	return err
}
