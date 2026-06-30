package handler

import (
	"net/http"

	log "github.com/sirupsen/logrus"
)

func (s *Handler) exchangeInfo(w http.ResponseWriter, r *http.Request) {
	data := s.srv.ExchangeInfo()
	if data == nil {
		log.Tracef("%s exchangeInfo cache unavailable, proxying via REST", s.class)
		s.reverseProxy(w, r)
		return
	}

	log.Tracef("%s exchangeInfo delivering via cache", s.class)
	if err := writeExchangeInfo(w, data); err != nil {
		log.Errorf("%s exchangeInfo response write failed: %v", s.class, err)
	}
}

func writeExchangeInfo(w http.ResponseWriter, data []byte) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "cache")
	_, err := w.Write(data)
	return err
}
