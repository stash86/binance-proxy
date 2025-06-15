package handler

import (
	"net/http"
)

func (s *Handler) exchangeInfo(w http.ResponseWriter) {
	data := s.srv.ExchangeInfo()
	if data == nil {
		http.Error(w, "ExchangeInfo not available", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Data-Source", "cache")
	w.Write(data)
}
