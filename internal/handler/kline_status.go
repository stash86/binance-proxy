package handler

import (
	"encoding/json"
	"net/http"
)

func (s *Handler) klineStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "only GET method allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if s.ctx.Err() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "service shutting down"})
		return
	}
	status := s.srv.KlineStatus()
	status.Class = s.class
	json.NewEncoder(w).Encode(status)
}
