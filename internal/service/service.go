package service

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	serviceCleanupInterval  = time.Second
	serviceDepthIdleExpiry  = 2 * time.Minute
	serviceTickerIdleExpiry = 2 * time.Minute
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
}

func NewService(ctx context.Context, class Class) *Service {
	s := &Service{class: class}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.exchangeInfoSrv = NewExchangeInfoSrv(s.ctx, newSymbolInterval(s.class, "", ""))
	s.exchangeInfoSrv.Start()

	go func() {
		ticker := time.NewTicker(serviceCleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.autoRemoveExpired()
			}
		}
	}()

	return s
}

func (s *Service) Stop() {
	s.cancel()
	if s.exchangeInfoSrv != nil {
		s.exchangeInfoSrv.Stop()
	}
	s.stopKlines()
	s.stopDepth()
	s.stopTickers()
}

func (s *Service) autoRemoveExpired() {
	now := time.Now() // Cache time.Now() call

	s.klinesSrv.Range(func(k, v interface{}) bool {
		si, ok := k.(symbolInterval)
		if !ok {
			return true
		}
		srv, ok := v.(*KlinesSrv)
		if !ok {
			s.klinesSrv.Delete(k)
			return true
		}

		last, ok := s.lastGetKlines.Load(si)
		if !ok {
			s.lastGetKlines.Store(si, now)
			return true
		}
		lastTime, ok := last.(time.Time)
		if !ok {
			s.lastGetKlines.Store(si, now)
			return true
		}

		interval, ok := intervalDuration(si.Interval)
		if !ok {
			log.Warnf("%s %s@%s kline websocket closed because interval is unknown.", si.Class, si.Symbol, si.Interval)
			s.deleteKline(si, srv)
			return true
		}
		expiry := 2 * interval
		if now.Sub(lastTime) > expiry {
			log.Debugf("%s %s@%s kline websocket closed after being idle for %.0fs.", si.Class, si.Symbol, si.Interval, expiry.Seconds())
			s.deleteKline(si, srv)
		}
		return true
	})
	s.depthSrv.Range(func(k, v interface{}) bool {
		si, ok := k.(symbolInterval)
		if !ok {
			return true
		}
		srv, ok := v.(*DepthSrv)
		if !ok {
			s.depthSrv.Delete(k)
			return true
		}

		last, ok := s.lastGetDepth.Load(si)
		if !ok {
			s.lastGetDepth.Store(si, now)
			return true
		}
		lastTime, ok := last.(time.Time)
		if !ok {
			s.lastGetDepth.Store(si, now)
			return true
		}

		if now.Sub(lastTime) > serviceDepthIdleExpiry {
			log.Debugf("%s %s depth websocket closed after being idle for %.0fs.", si.Class, si.Symbol, serviceDepthIdleExpiry.Seconds())
			s.deleteDepth(si, srv)
		}
		return true
	})
	s.tickerSrv.Range(func(k, v interface{}) bool {
		si, ok := k.(symbolInterval)
		if !ok {
			return true
		}
		srv, ok := v.(*TickerSrv)
		if !ok {
			s.tickerSrv.Delete(k)
			return true
		}

		last, ok := s.lastGetTicker.Load(si)
		if !ok {
			s.lastGetTicker.Store(si, now)
			return true
		}
		lastTime, ok := last.(time.Time)
		if !ok {
			s.lastGetTicker.Store(si, now)
			return true
		}

		if now.Sub(lastTime) > serviceTickerIdleExpiry {
			log.Debugf("%s %s ticker24hr websocket closed after being idle for %.0fs.", si.Class, si.Symbol, serviceTickerIdleExpiry.Seconds())
			s.deleteTicker(si, srv)
		}
		return true
	})
}

func (s *Service) Ticker(symbol string) *Ticker24hr {
	if s.ctx.Err() != nil {
		return nil
	}
	si := newSymbolInterval(s.class, symbol, "")
	srv, loaded := s.tickerSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.tickerSrv.LoadOrStore(*si, NewTickerSrv(s.ctx, si)); !loaded {
			srv.(*TickerSrv).Start()
		}
	}
	s.lastGetTicker.Store(*si, time.Now())

	return srv.(*TickerSrv).GetTicker()
}

func (s *Service) ExchangeInfo() []byte {
	if s.ctx.Err() != nil {
		return nil
	}
	return s.exchangeInfoSrv.GetExchangeInfo()
}

func (s *Service) Klines(symbol, interval string) []*Kline {
	if s.ctx.Err() != nil {
		return nil
	}
	si := newSymbolInterval(s.class, symbol, interval)
	srv, loaded := s.klinesSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.klinesSrv.LoadOrStore(*si, NewKlinesSrv(s.ctx, si)); !loaded {
			srv.(*KlinesSrv).Start()
		}
	}
	s.lastGetKlines.Store(*si, time.Now())

	return srv.(*KlinesSrv).GetKlines()
}

func (s *Service) Depth(symbol string) *Depth {
	if s.ctx.Err() != nil {
		return nil
	}
	si := newSymbolInterval(s.class, symbol, "")
	srv, loaded := s.depthSrv.Load(*si)
	if !loaded {
		if srv, loaded = s.depthSrv.LoadOrStore(*si, NewDepthSrv(s.ctx, si)); !loaded {
			srv.(*DepthSrv).Start()
		}
	}
	s.lastGetDepth.Store(*si, time.Now())

	return srv.(*DepthSrv).GetDepth()
}

func (s *Service) deleteKline(si symbolInterval, srv *KlinesSrv) {
	s.lastGetKlines.Delete(si)
	s.klinesSrv.Delete(si)
	srv.Stop()
}

func (s *Service) deleteDepth(si symbolInterval, srv *DepthSrv) {
	s.lastGetDepth.Delete(si)
	s.depthSrv.Delete(si)
	srv.Stop()
}

func (s *Service) deleteTicker(si symbolInterval, srv *TickerSrv) {
	s.lastGetTicker.Delete(si)
	s.tickerSrv.Delete(si)
	srv.Stop()
}

func (s *Service) stopKlines() {
	s.klinesSrv.Range(func(k, v interface{}) bool {
		if si, ok := k.(symbolInterval); ok {
			s.lastGetKlines.Delete(si)
		}
		if srv, ok := v.(*KlinesSrv); ok {
			srv.Stop()
		}
		s.klinesSrv.Delete(k)
		return true
	})
}

func (s *Service) stopDepth() {
	s.depthSrv.Range(func(k, v interface{}) bool {
		if si, ok := k.(symbolInterval); ok {
			s.lastGetDepth.Delete(si)
		}
		if srv, ok := v.(*DepthSrv); ok {
			srv.Stop()
		}
		s.depthSrv.Delete(k)
		return true
	})
}

func (s *Service) stopTickers() {
	s.tickerSrv.Range(func(k, v interface{}) bool {
		if si, ok := k.(symbolInterval); ok {
			s.lastGetTicker.Delete(si)
		}
		if srv, ok := v.(*TickerSrv); ok {
			srv.Stop()
		}
		s.tickerSrv.Delete(k)
		return true
	})
}
