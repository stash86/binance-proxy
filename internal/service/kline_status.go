package service

import (
	"sort"
	"time"
)

// KlineStatus reports eligibility of the existing WS candle caches. It does not
// describe REST availability or start/refresh subscriptions when polled.
type KlineStatus struct {
	Class        Class               `json:"class"`
	Ready        bool                `json:"ready"`
	Total        int                 `json:"total"`
	Fresh        int                 `json:"fresh"`
	Stale        int                 `json:"stale"`
	Initializing int                 `json:"initializing"`
	Recovering   int                 `json:"recovering"`
	Series       []KlineSeriesStatus `json:"series"`
}

type KlineSeriesStatus struct {
	Symbol      string `json:"symbol"`
	Interval    string `json:"interval"`
	State       string `json:"state"`
	Reason      string `json:"reason"`
	UpdateAgeMS *int64 `json:"update_age_ms"`
	EventAgeMS  *int64 `json:"event_age_ms"`
}

func (s *Service) KlineStatus() KlineStatus {
	status := KlineStatus{Series: []KlineSeriesStatus{}}
	if s == nil {
		return status
	}
	status.Class = s.class
	now := time.Now()
	s.klinesSrv.Range(func(_, value interface{}) bool {
		srv, ok := value.(*KlinesSrv)
		if !ok || srv == nil {
			return true
		}
		item := srv.klineStatus(now)
		status.Series = append(status.Series, item)
		switch item.State {
		case "fresh":
			status.Fresh++
		case "stale":
			status.Stale++
		case "initializing":
			status.Initializing++
		case "recovering":
			status.Recovering++
		}
		return true
	})
	sort.Slice(status.Series, func(i, j int) bool {
		a, b := status.Series[i], status.Series[j]
		if a.Symbol == b.Symbol {
			return a.Interval < b.Interval
		}
		return a.Symbol < b.Symbol
	})
	status.Total = len(status.Series)
	status.Ready = status.Total > 0 && status.Fresh == status.Total && s.ctx.Err() == nil
	return status
}

func (s *KlinesSrv) klineStatus(now time.Time) KlineSeriesStatus {
	s.rw.RLock()
	defer s.rw.RUnlock()
	item := KlineSeriesStatus{Symbol: s.si.Symbol, Interval: s.si.Interval}
	if !s.lastUpdate.IsZero() {
		age := max(int64(0), now.Sub(s.lastUpdate).Milliseconds())
		item.UpdateAgeMS = &age
	}
	if s.lastEvent > 0 {
		age := max(int64(0), now.UnixMilli()-s.lastEvent)
		item.EventAgeMS = &age
	}
	switch {
	case s.ctx.Err() != nil:
		item.State, item.Reason = "stale", "stopped"
	case s.freshLocked(now):
		item.State, item.Reason = "fresh", "fresh"
	case len(s.klinesArr) == 0 || s.lastUpdate.IsZero():
		item.State, item.Reason = "initializing", "no_history"
		if !s.recoveryAt.IsZero() {
			item.State, item.Reason = "recovering", "rebuilding_history"
		}
		if len(s.klinesArr) > 0 {
			item.Reason = "awaiting_ws_event"
		}
	case now.Sub(s.lastUpdate) > klineFreshnessTimeout:
		item.State, item.Reason = "stale", "update_stale"
	case s.lastEvent < now.Add(-klineFreshnessTimeout).UnixMilli():
		item.State, item.Reason = "stale", "event_stale"
	default:
		item.State, item.Reason = "stale", "candle_not_current"
	}
	return item
}
