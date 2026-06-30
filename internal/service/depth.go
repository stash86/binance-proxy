package service

import (
	"binance-proxy/internal/tool"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	spot "github.com/adshao/go-binance/v2"
	futures "github.com/adshao/go-binance/v2/futures"
)

const (
	depthLevels         = 20
	depthUpdateInterval = 100 * time.Millisecond
	depthInitTimeout    = 2 * time.Second
)

type DepthSrv struct {
	rw sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	initCtx  context.Context
	initDone context.CancelFunc

	si    *symbolInterval
	depth *Depth
}

type Depth struct {
	LastUpdateID int64
	Time         int64
	TradeTime    int64
	Bids         []futures.Bid
	Asks         []futures.Ask
}

func NewDepthSrv(ctx context.Context, si *symbolInterval) *DepthSrv {
	s := &DepthSrv{si: si}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.initCtx, s.initDone = context.WithCancel(context.Background())

	return s
}

func (s *DepthSrv) Start() {
	go func() {
		for d := tool.NewDelayIterator(); ; {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			s.clearDepth()

			doneC, stopC, err := s.connect()
			if err != nil {
				log.Errorf("%s %s depth websocket connection error: %s.", s.si.Class, s.si.Symbol, err)
				if !d.DelayContext(s.ctx) {
					return
				}
				continue
			}

			log.Debugf("%s %s depth websocket connected.", s.si.Class, s.si.Symbol)
			// Reset the reconnect backoff now that we have a successful connection
			d.Reset()
			select {
			case <-s.ctx.Done():
				s.stopWebsocket(stopC)
				return
			case <-doneC:
			}

			log.Warnf("%s %s depth websocket disconnected, trying to reconnect.", s.si.Class, s.si.Symbol)
			if !d.DelayContext(s.ctx) {
				return
			}
		}
	}()
}

func (s *DepthSrv) Stop() {
	s.cancel()
}

func (s *DepthSrv) connect() (doneC, stopC chan struct{}, err error) {
	if s.si.Class == SPOT {
		return spot.WsPartialDepthServe100Ms(s.si.Symbol, strconv.Itoa(depthLevels), s.wsHandler, s.errHandler)
	}
	return futures.WsPartialDepthServeWithRate(s.si.Symbol, depthLevels, depthUpdateInterval, s.wsHandlerFutures, s.errHandler)
}

func (s *DepthSrv) GetDepth() *Depth {
	if !s.waitForInit() {
		return nil
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	return cloneDepth(s.depth)
}

func (s *DepthSrv) waitForInit() bool {
	select {
	case <-s.initCtx.Done():
		return true
	default:
	}

	timer := time.NewTimer(depthInitTimeout)
	defer timer.Stop()

	select {
	case <-s.initCtx.Done():
		return true
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func (s *DepthSrv) wsHandlerFutures(event *futures.WsDepthEvent) {
	if event == nil {
		return
	}

	s.rw.Lock()
	defer s.rw.Unlock()

	if s.depth == nil {
		defer s.initDone()
	}

	s.depth = &Depth{
		LastUpdateID: event.LastUpdateID,
		Time:         event.Time,
		TradeTime:    event.TransactionTime,
		Bids:         event.Bids,
		Asks:         event.Asks,
	}
	log.Tracef("%s %s depth websocket message received", s.si.Class, s.si.Symbol)
}

func (s *DepthSrv) wsHandler(event *spot.WsPartialDepthEvent) {
	if event == nil {
		return
	}

	now := time.Now().UnixMilli()

	s.rw.Lock()
	defer s.rw.Unlock()

	if s.depth == nil {
		defer s.initDone()
	}

	s.depth = &Depth{
		LastUpdateID: event.LastUpdateID,
		Time:         now,
		TradeTime:    now,
		Bids:         event.Bids,
		Asks:         event.Asks,
	}
	log.Tracef("%s %s depth websocket message received", s.si.Class, s.si.Symbol)

}

func (s *DepthSrv) errHandler(err error) {
	if err == nil {
		return
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled"):
		log.Warnf("%s %s depth websocket context canceled, will restart connection.", s.si.Class, s.si.Symbol)
	case strings.Contains(msg, "use of closed network connection"):
		// This commonly indicates a normal remote close/rotation; treat as info/debug to reduce noise
		log.Infof("%s %s depth websocket closed by peer; reconnecting.", s.si.Class, s.si.Symbol)
	default:
		log.Errorf("%s %s depth websocket connection error: %s.", s.si.Class, s.si.Symbol, err)
	}
}

func (s *DepthSrv) clearDepth() {
	s.rw.Lock()
	defer s.rw.Unlock()

	s.depth = nil
}

func (s *DepthSrv) stopWebsocket(stopC chan struct{}) {
	if stopC == nil {
		return
	}

	select {
	case stopC <- struct{}{}:
	case <-time.After(time.Second):
		log.Debugf("%s %s depth websocket stop signal timed out.", s.si.Class, s.si.Symbol)
	}
}

func cloneDepth(depth *Depth) *Depth {
	if depth == nil {
		return nil
	}

	cloned := *depth
	cloned.Bids = append([]futures.Bid(nil), depth.Bids...)
	cloned.Asks = append([]futures.Ask(nil), depth.Asks...)
	return &cloned
}
