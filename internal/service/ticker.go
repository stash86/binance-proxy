package service

import (
	"binance-proxy/internal/tool"
	"context"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	spot "github.com/adshao/go-binance/v2"
)

const (
	tickerInitTimeout = 2 * time.Second
	tickerStopTimeout = time.Second
)

type TickerSrv struct {
	rw sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	initCtx  context.Context
	initDone context.CancelFunc

	si         *symbolInterval
	ticker24hr *Ticker24hr
	bookTicker *BookTicker
}

type BookTicker struct {
	Symbol      string `json:"symbol"`
	BidPrice    string `json:"bidPrice"`
	BidQuantity string `json:"bidQty"`
	AskPrice    string `json:"askPrice"`
	AskQuantity string `json:"askQty"`
}

type Ticker24hr struct {
	Symbol             string `json:"symbol"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	WeightedAvgPrice   string `json:"weightedAvgPrice"`
	PrevClosePrice     string `json:"prevClosePrice"`
	LastPrice          string `json:"lastPrice"`
	LastQty            string `json:"lastQty"`
	BidPrice           string `json:"bidPrice"`
	AskPrice           string `json:"askPrice"`
	OpenPrice          string `json:"openPrice"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
	OpenTime           int64  `json:"openTime"`
	CloseTime          int64  `json:"closeTime"`
	FirstID            int64  `json:"firstId"`
	LastID             int64  `json:"lastId"`
	Count              int64  `json:"count"`
}

func NewTickerSrv(ctx context.Context, si *symbolInterval) *TickerSrv {
	s := &TickerSrv{si: si}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.initCtx, s.initDone = context.WithCancel(context.Background())

	return s
}

func (s *TickerSrv) Start() {
	go func() {
		for d := tool.NewDelayIterator(); ; {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			s.clearTickers()

			ticker24hrDoneC, ticker24hrstopC, err := s.connectTicker24hr()
			if err != nil {
				log.Errorf("%s %s ticker24hr websocket connection error: %s.", s.si.Class, s.si.Symbol, err)
				if !d.DelayContext(s.ctx) {
					return
				}
				continue
			}

			bookDoneC, bookStopC, err := s.connectTickerBook()
			if err != nil {
				s.stopWebsocket(ticker24hrstopC)
				log.Errorf("%s %s bookTicker websocket connection error: %s.", s.si.Class, s.si.Symbol, err)
				if !d.DelayContext(s.ctx) {
					return
				}
				continue
			}

			log.Debugf("%s %s ticker24hr and bookTicker websocket connected.", s.si.Class, s.si.Symbol)
			d.Reset()
			select {
			case <-s.ctx.Done():
				s.stopWebsocket(bookStopC)
				s.stopWebsocket(ticker24hrstopC)
				return
			case <-bookDoneC:
				s.stopWebsocket(ticker24hrstopC)
			case <-ticker24hrDoneC:
				s.stopWebsocket(bookStopC)
			}

			log.Warnf("%s %s ticker24hr or bookTicker websocket disconnected, trying to reconnect.", s.si.Class, s.si.Symbol)
			if !d.DelayContext(s.ctx) {
				return
			}
		}
	}()
}

func (s *TickerSrv) Stop() {
	s.cancel()
}

func (s *TickerSrv) connectTickerBook() (doneC, stopC chan struct{}, err error) {
	return spot.WsBookTickerServe(s.si.Symbol, s.wsHandlerBookTicker, s.errHandler)
}

func (s *TickerSrv) connectTicker24hr() (doneC, stopC chan struct{}, err error) {
	return spot.WsMarketStatServe(s.si.Symbol, s.wsHandlerTicker24hr, s.errHandler)
}

func (s *TickerSrv) GetTicker() *Ticker24hr {
	if !s.waitForInit() {
		return nil
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	if s.ticker24hr == nil {
		return nil
	}

	bidPrice := s.ticker24hr.BidPrice
	askPrice := s.ticker24hr.AskPrice
	if s.bookTicker != nil {
		bidPrice = s.bookTicker.BidPrice
		askPrice = s.bookTicker.AskPrice
	}

	return &Ticker24hr{
		Symbol:             s.ticker24hr.Symbol,
		PriceChange:        s.ticker24hr.PriceChange,
		PriceChangePercent: s.ticker24hr.PriceChangePercent,
		WeightedAvgPrice:   s.ticker24hr.WeightedAvgPrice,
		PrevClosePrice:     s.ticker24hr.PrevClosePrice,
		LastPrice:          s.ticker24hr.LastPrice,
		LastQty:            s.ticker24hr.LastQty,
		BidPrice:           bidPrice,
		AskPrice:           askPrice,
		OpenPrice:          s.ticker24hr.OpenPrice,
		HighPrice:          s.ticker24hr.HighPrice,
		LowPrice:           s.ticker24hr.LowPrice,
		Volume:             s.ticker24hr.Volume,
		QuoteVolume:        s.ticker24hr.QuoteVolume,
		OpenTime:           s.ticker24hr.OpenTime,
		CloseTime:          s.ticker24hr.CloseTime,
		FirstID:            s.ticker24hr.FirstID,
		LastID:             s.ticker24hr.LastID,
		Count:              s.ticker24hr.Count,
	}
}

func (s *TickerSrv) wsHandlerBookTicker(event *spot.WsBookTickerEvent) {
	if event == nil {
		return
	}

	s.rw.Lock()
	defer s.rw.Unlock()

	s.bookTicker = &BookTicker{
		Symbol:      event.Symbol,
		BidPrice:    event.BestBidPrice,
		BidQuantity: event.BestBidQty,
		AskPrice:    event.BestAskPrice,
		AskQuantity: event.BestAskQty,
	}
	log.Tracef("%s %s bookTicker websocket message received", s.si.Class, s.si.Symbol)
}

func (s *TickerSrv) wsHandlerTicker24hr(event *spot.WsMarketStatEvent) {
	if event == nil {
		return
	}

	s.rw.Lock()
	defer s.rw.Unlock()

	if s.ticker24hr == nil {
		defer s.initDone()
	}

	s.ticker24hr = &Ticker24hr{
		Symbol:             event.Symbol,
		PriceChange:        event.PriceChange,
		PriceChangePercent: event.PriceChangePercent,
		WeightedAvgPrice:   event.WeightedAvgPrice,
		PrevClosePrice:     event.PrevClosePrice,
		LastPrice:          event.LastPrice,
		LastQty:            event.CloseQty,
		BidPrice:           event.BidPrice,
		AskPrice:           event.AskPrice,
		OpenPrice:          event.OpenPrice,
		HighPrice:          event.HighPrice,
		LowPrice:           event.LowPrice,
		Volume:             event.BaseVolume,
		QuoteVolume:        event.QuoteVolume,
		OpenTime:           event.OpenTime,
		CloseTime:          event.CloseTime,
		FirstID:            event.FirstID,
		LastID:             event.LastID,
		Count:              event.Count,
	}
	log.Tracef("%s %s ticker24hr websocket message received", s.si.Class, s.si.Symbol)
}

func (s *TickerSrv) errHandler(err error) {
	if err == nil {
		return
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled"):
		log.Warnf("%s %s ticker websocket context canceled, will restart connection.", s.si.Class, s.si.Symbol)
	case strings.Contains(msg, "use of closed network connection"):
		log.Infof("%s %s ticker websocket closed by peer; reconnecting.", s.si.Class, s.si.Symbol)
	default:
		log.Errorf("%s %s ticker24hr websocket connection error: %s.", s.si.Class, s.si.Symbol, err)
	}
}

func (s *TickerSrv) waitForInit() bool {
	select {
	case <-s.initCtx.Done():
		return true
	default:
	}

	timer := time.NewTimer(tickerInitTimeout)
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

func (s *TickerSrv) clearTickers() {
	s.rw.Lock()
	defer s.rw.Unlock()

	s.ticker24hr = nil
	s.bookTicker = nil
}

func (s *TickerSrv) stopWebsocket(stopC chan struct{}) {
	if stopC == nil {
		return
	}

	select {
	case stopC <- struct{}{}:
	case <-time.After(tickerStopTimeout):
		log.Debugf("%s %s ticker websocket stop signal timed out.", s.si.Class, s.si.Symbol)
	}
}
