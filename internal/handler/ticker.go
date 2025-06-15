package handler

import (
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s *Handler) ticker(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")

	if symbol == "" {
		log.Tracef("%s ticker24hr without symbol request proxying via REST", s.class)
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

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "websocket")

	buf := GetBuffer()
	defer PutBuffer(buf)

	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(ticker); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	w.Write(buf.Bytes())
}
