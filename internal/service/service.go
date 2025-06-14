package service

import (
	"context"
	"sync"
	"time"

	"binance-proxy/internal/metrics"

	log "github.com/sirupsen/logrus"
)

type Service struct {
	ctx    context.Context
	cancel context.CancelFunc

	class           Class
	exchangeInfoSrv *ExchangeInfoSrv
	klinesSrv       sync.Map // map[symbolInterval]*KlinesSrv
	depthSrv        sync.Map // map[symbolInterval]*DepthSrv
	tickerSrv       sync.Map // map[symbolInterval]*TickerSrv

	lastGetKlines sync.Map // map[symbolInterval]time.Time
	lastGetDepth  sync.Map // map[symbolInterval]time.Time
	lastGetTicker sync.Map // map[symbolInterval]time.Time

	// Resource management
	cleanupTicker *time.Ticker
	metrics       *metrics.Metrics
}

func NewService(ctx context.Context, class Class) *Service {
	s := &Service{
		class:   class,
		metrics: metrics.GetMetrics(),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.exchangeInfoSrv = NewExchangeInfoSrv(s.ctx, NewSymbolInterval(s.class, "", ""))
	s.exchangeInfoSrv.Start()

	// Start cleanup routine with more reasonable interval
	s.cleanupTicker = time.NewTicker(30 * time.Second)
	go s.cleanupRoutine()

	return s
}

func (s *Service) cleanupRoutine() {
	defer s.cleanupTicker.Stop()
	
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.cleanupTicker.C:
			s.autoRemoveExpired()
		}
	}
}

func (s *Service) Stop() {
	log.Infof("%s service shutting down", s.class)
	s.cancel()
	
	// Stop all services
	s.klinesSrv.Range(func(k, v interface{}) bool {
		srv := v.(*KlinesSrv)
		srv.Stop()
		return true
	})
	
	s.depthSrv.Range(func(k, v interface{}) bool {
		srv := v.(*DepthSrv)
		srv.Stop()
		return true
	})
	
	s.tickerSrv.Range(func(k, v interface{}) bool {
		srv := v.(*TickerSrv)
		srv.Stop()
		return true
	})
	
	s.exchangeInfoSrv.Stop()
	log.Infof("%s service shutdown complete", s.class)
}

func (s *Service) autoRemoveExpired() {
	s.klinesSrv.Range(func(k, v interface{}) bool {
		si := k.(symbolInterval)
		srv := v.(*KlinesSrv)

		if t, ok := s.lastGetKlines.Load(si); ok {
			expiry := 2 * INTERVAL_2_DURATION[si.Interval]
			if time.Now().Sub(t.(time.Time)) > expiry {
				// log.Debugf("%s.Kline srv expired!Removed %d", si, expiry)
				log.Debugf("%s %s@%s kline websocket closed after being idle for %.0fs.", si.Class, si.Symbol, si.Interval, expiry.Seconds())
				s.lastGetKlines.Delete(si)

				s.klinesSrv.Delete(si)
				srv.Stop()
			}
		} else {
			s.lastGetKlines.Store(si, time.Now())
		}

		return true
	})
	s.depthSrv.Range(func(k, v interface{}) bool {
		si := k.(symbolInterval)
		srv := v.(*DepthSrv)

		if t, ok := s.lastGetDepth.Load(si); ok {
			expiry := 2 * time.Minute
			if time.Now().Sub(t.(time.Time)) > expiry {
				log.Debugf("%s %s depth websocket closed after being idle for %.0fs.", si.Class, si.Symbol, expiry.Seconds())
				s.lastGetDepth.Delete(si)

				s.depthSrv.Delete(si)
				srv.Stop()
			}
		} else {
			s.lastGetDepth.Store(si, time.Now())
		}

		return true
	})
	s.tickerSrv.Range(func(k, v interface{}) bool {
		si := k.(symbolInterval)
		srv := v.(*TickerSrv)

		if t, ok := s.lastGetTicker.Load(si); ok {
			expiry := 2 * time.Minute
			if time.Now().Sub(t.(time.Time)) > expiry {
				// log.Debugf("%s.Ticker srv expired!Removed", si)
				log.Debugf("%s %s ticker24hr websocket closed after being idle for %.0fs.", si.Class, si.Symbol, expiry.Seconds())
				s.lastGetTicker.Delete(si)

				s.tickerSrv.Delete(si)
				srv.Stop()
			}
		} else {
			s.lastGetTicker.Store(si, time.Now())
		}

		return true
	})
}

func (s *Service) Ticker(symbol string) *Ticker24hr {
	si := NewSymbolInterval(s.class, symbol, "")
	srv, loaded := s.tickerSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.tickerSrv.LoadOrStore(*si, NewTickerSrv(s.ctx, si)); loaded == false {
			srv.(*TickerSrv).Start()
		}
	}
	s.lastGetTicker.Store(*si, time.Now())

	return srv.(*TickerSrv).GetTicker()
}

func (s *Service) ExchangeInfo() []byte {
	return s.exchangeInfoSrv.GetExchangeInfo()
}

func (s *Service) Klines(symbol, interval string) []*Kline {
	si := NewSymbolInterval(s.class, symbol, interval)
	srv, loaded := s.klinesSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.klinesSrv.LoadOrStore(*si, NewKlinesSrv(s.ctx, si)); loaded == false {
			srv.(*KlinesSrv).Start()
		}
	}
	s.lastGetKlines.Store(*si, time.Now())

	return srv.(*KlinesSrv).GetKlines()
}

func (s *Service) Depth(symbol string) *Depth {
	si := NewSymbolInterval(s.class, symbol, "")
	srv, loaded := s.depthSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.depthSrv.LoadOrStore(*si, NewDepthSrv(s.ctx, si)); loaded == false {
			srv.(*DepthSrv).Start()
		}
	}
	s.lastGetDepth.Store(*si, time.Now())

	return srv.(*DepthSrv).GetDepth()
}
