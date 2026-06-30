package service

import (
	"binance-proxy/internal/tool"
	"container/list"
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	spot "github.com/adshao/go-binance/v2"
	futures "github.com/adshao/go-binance/v2/futures"
)

const (
	klineHistoryLimit = 1000
	klineInitTimeout  = 2 * time.Second
	klineStopTimeout  = time.Second
)

type Kline struct {
	OpenTime                 int64
	Open                     string
	High                     string
	Low                      string
	Close                    string
	Volume                   string
	CloseTime                int64
	QuoteAssetVolume         string
	TradeNum                 int64
	TakerBuyBaseAssetVolume  string
	TakerBuyQuoteAssetVolume string
}

type KlinesSrv struct {
	rw sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	initCtx  context.Context
	initDone context.CancelFunc

	si         *symbolInterval
	klinesList *list.List
	klinesArr  []*Kline
}

func NewKlinesSrv(ctx context.Context, si *symbolInterval) *KlinesSrv {
	s := &KlinesSrv{si: si}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.initCtx, s.initDone = context.WithCancel(context.Background())

	return s
}

func (s *KlinesSrv) Start() {
	go func() {
		for d := tool.NewDelayIterator(); ; {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			s.clearKlineList()

			doneC, stopC, err := s.connect()
			if err != nil {
				log.Errorf("%s %s@%s kline websocket connection error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
				if !d.DelayContext(s.ctx) {
					return
				}
				continue
			}

			log.Debugf("%s %s@%s kline websocket connected.", s.si.Class, s.si.Symbol, s.si.Interval)
			d.Reset()
			select {
			case <-s.ctx.Done():
				s.stopWebsocket(stopC)
				return
			case <-doneC:
			}
			log.Warnf("%s %s@%s kline websocket disconnected, trying to reconnect.", s.si.Class, s.si.Symbol, s.si.Interval)
			if !d.DelayContext(s.ctx) {
				return
			}
		}
	}()
}

func (s *KlinesSrv) Stop() {
	s.cancel()
}

func (s *KlinesSrv) errHandler(err error) {
	if err == nil {
		return
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context canceled"):
		log.Warnf("%s %s@%s kline websocket context canceled, will restart connection.", s.si.Class, s.si.Symbol, s.si.Interval)
	case strings.Contains(msg, "use of closed network connection"):
		log.Infof("%s %s@%s kline websocket closed by peer; reconnecting.", s.si.Class, s.si.Symbol, s.si.Interval)
	default:
		log.Errorf("%s %s@%s kline websocket connection error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
	}
}

func (s *KlinesSrv) connect() (doneC, stopC chan struct{}, err error) {
	if s.si.Class == SPOT {
		return spot.WsKlineServe(s.si.Symbol,
			s.si.Interval,
			func(event *spot.WsKlineEvent) { s.wsHandler(event) },
			s.errHandler,
		)
	} else {
		return futures.WsKlineServe(s.si.Symbol,
			s.si.Interval,
			func(event *futures.WsKlineEvent) { s.wsHandler(event) },
			s.errHandler,
		)
	}
}

func (s *KlinesSrv) initKlineData() {
	// Check if API is banned
	banDetector := GetBanDetector()
	if banDetector.IsBanned(s.si.Class) {
		log.Debugf("%s %s@%s kline initialization skipped due to API ban", s.si.Class, s.si.Symbol, s.si.Interval)

		// Create empty klines list to prevent repeated initialization attempts
		s.setKlineList(list.New(), false)
		s.initDone()
		return
	}

	var klines interface{}
	var err error
	log.Debugf("%s %s@%s kline initialization through REST.", s.si.Class, s.si.Symbol, s.si.Interval)
	for d := tool.NewDelayIterator(); ; {
		if err := s.ctx.Err(); err != nil {
			s.setKlineList(list.New(), false)
			s.initDone()
			return
		}

		// Check ban status before each attempt
		if banDetector.IsBanned(s.si.Class) {
			log.Debugf("%s %s@%s kline initialization stopped due to API ban", s.si.Class, s.si.Symbol, s.si.Interval)
			s.setKlineList(list.New(), false)
			s.initDone()
			return
		}

		var resp *http.Response
		if s.si.Class == SPOT {
			if err := RateWait(s.ctx, s.si.Class, http.MethodGet, "/api/v3/klines", url.Values{
				"limit": []string{"1000"},
			}); err != nil {
				s.setKlineList(list.New(), false)
				s.initDone()
				return
			}
			if err := s.ctx.Err(); err != nil {
				s.setKlineList(list.New(), false)
				s.initDone()
				return
			}
			client := spot.NewClient("", "")
			klines, err = client.NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(1000).
				Do(s.ctx)
		} else {
			if err := RateWait(s.ctx, s.si.Class, http.MethodGet, "/fapi/v1/klines", url.Values{
				"limit": []string{"1000"},
			}); err != nil {
				s.setKlineList(list.New(), false)
				s.initDone()
				return
			}
			if err := s.ctx.Err(); err != nil {
				s.setKlineList(list.New(), false)
				s.initDone()
				return
			}
			client := futures.NewClient("", "")
			klines, err = client.NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(1000).
				Do(s.ctx)
		}

		// Check for bans (resp might be nil for SDK calls, so we check err)
		if banDetector.CheckResponse(s.si.Class, resp, err) {
			log.Debugf("%s %s@%s kline initialization stopped due to detected ban", s.si.Class, s.si.Symbol, s.si.Interval)
			s.setKlineList(list.New(), false)
			s.initDone()
			return
		}

		if err != nil {
			log.Errorf("%s %s@%s kline initialization via REST failed, error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
			if !d.DelayContext(s.ctx) {
				s.setKlineList(list.New(), false)
				s.initDone()
				return
			}
			continue
		}

		klinesList := list.New()

		if vi, ok := klines.([]*spot.Kline); ok {
			for _, v := range vi {
				if v == nil {
					continue
				}

				klinesList.PushBack(klineFromSpotREST(v))
			}
		} else if vi, ok := klines.([]*futures.Kline); ok {
			for _, v := range vi {
				if v == nil {
					continue
				}

				klinesList.PushBack(klineFromFuturesREST(v))
			}
		}

		s.setKlineList(klinesList, true)
		s.initDone()
		return
	}
}

func (s *KlinesSrv) wsHandler(event interface{}) {
	k, ok := klineFromWSEvent(event)
	if !ok {
		return
	}

	if s.getKlineList() == nil {
		s.initKlineData()
	}

	log.Tracef("%s %s@%s kline websocket message received for open timestamp %d", s.si.Class, s.si.Symbol, s.si.Interval, k.OpenTime)

	s.rw.Lock()
	defer s.rw.Unlock()

	if s.klinesList == nil {
		s.klinesList = list.New()
	}

	if s.klinesList.Len() == 0 {
		s.klinesList.PushBack(k)
	} else {
		last := s.klinesList.Back().Value.(*Kline)
		if last.OpenTime < k.OpenTime {
			s.klinesList.PushBack(k)
		} else if last.OpenTime == k.OpenTime {
			s.klinesList.Back().Value = k
		}
	}

	for s.klinesList.Len() > klineHistoryLimit {
		s.klinesList.Remove(s.klinesList.Front())
	}

	s.klinesArr = klinesFromList(s.klinesList)
	s.initDone()
}

func (s *KlinesSrv) GetKlines() []*Kline {
	if !s.waitForInit() {
		return nil
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	return cloneKlines(s.klinesArr)
}

func (s *KlinesSrv) waitForInit() bool {
	select {
	case <-s.initCtx.Done():
		return true
	default:
	}

	timer := time.NewTimer(klineInitTimeout)
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

func (s *KlinesSrv) clearKlineList() {
	s.rw.Lock()
	defer s.rw.Unlock()

	s.klinesList = nil
}

func (s *KlinesSrv) getKlineList() *list.List {
	s.rw.RLock()
	defer s.rw.RUnlock()

	return s.klinesList
}

func (s *KlinesSrv) setKlineList(klinesList *list.List, updateArr bool) {
	s.rw.Lock()
	defer s.rw.Unlock()

	s.klinesList = klinesList
	if updateArr {
		s.klinesArr = klinesFromList(klinesList)
	}
}

func (s *KlinesSrv) stopWebsocket(stopC chan struct{}) {
	if stopC == nil {
		return
	}

	select {
	case stopC <- struct{}{}:
	case <-time.After(klineStopTimeout):
		log.Debugf("%s %s@%s kline websocket stop signal timed out.", s.si.Class, s.si.Symbol, s.si.Interval)
	}
}

func klineFromSpotREST(v *spot.Kline) *Kline {
	return &Kline{
		OpenTime:                 v.OpenTime,
		Open:                     v.Open,
		High:                     v.High,
		Low:                      v.Low,
		Close:                    v.Close,
		Volume:                   v.Volume,
		CloseTime:                v.CloseTime,
		QuoteAssetVolume:         v.QuoteAssetVolume,
		TradeNum:                 v.TradeNum,
		TakerBuyBaseAssetVolume:  v.TakerBuyBaseAssetVolume,
		TakerBuyQuoteAssetVolume: v.TakerBuyQuoteAssetVolume,
	}
}

func klineFromFuturesREST(v *futures.Kline) *Kline {
	return &Kline{
		OpenTime:                 v.OpenTime,
		Open:                     v.Open,
		High:                     v.High,
		Low:                      v.Low,
		Close:                    v.Close,
		Volume:                   v.Volume,
		CloseTime:                v.CloseTime,
		QuoteAssetVolume:         v.QuoteAssetVolume,
		TradeNum:                 v.TradeNum,
		TakerBuyBaseAssetVolume:  v.TakerBuyBaseAssetVolume,
		TakerBuyQuoteAssetVolume: v.TakerBuyQuoteAssetVolume,
	}
}

func klineFromWSEvent(event interface{}) (*Kline, bool) {
	switch vi := event.(type) {
	case *spot.WsKlineEvent:
		if vi == nil {
			return nil, false
		}
		return &Kline{
			OpenTime:                 vi.Kline.StartTime,
			Open:                     vi.Kline.Open,
			High:                     vi.Kline.High,
			Low:                      vi.Kline.Low,
			Close:                    vi.Kline.Close,
			Volume:                   vi.Kline.Volume,
			CloseTime:                vi.Kline.EndTime,
			QuoteAssetVolume:         vi.Kline.QuoteVolume,
			TradeNum:                 vi.Kline.TradeNum,
			TakerBuyBaseAssetVolume:  vi.Kline.ActiveBuyVolume,
			TakerBuyQuoteAssetVolume: vi.Kline.ActiveBuyQuoteVolume,
		}, true
	case *futures.WsKlineEvent:
		if vi == nil {
			return nil, false
		}
		return &Kline{
			OpenTime:                 vi.Kline.StartTime,
			Open:                     vi.Kline.Open,
			High:                     vi.Kline.High,
			Low:                      vi.Kline.Low,
			Close:                    vi.Kline.Close,
			Volume:                   vi.Kline.Volume,
			CloseTime:                vi.Kline.EndTime,
			QuoteAssetVolume:         vi.Kline.QuoteVolume,
			TradeNum:                 vi.Kline.TradeNum,
			TakerBuyBaseAssetVolume:  vi.Kline.ActiveBuyVolume,
			TakerBuyQuoteAssetVolume: vi.Kline.ActiveBuyQuoteVolume,
		}, true
	default:
		return nil, false
	}
}

func klinesFromList(klinesList *list.List) []*Kline {
	if klinesList == nil {
		return nil
	}

	klinesArr := make([]*Kline, klinesList.Len())
	i := 0
	for elems := klinesList.Front(); elems != nil; elems = elems.Next() {
		klinesArr[i] = elems.Value.(*Kline)
		i++
	}

	return klinesArr
}

func cloneKlines(klines []*Kline) []*Kline {
	if klines == nil {
		return nil
	}

	cloned := make([]*Kline, len(klines))
	for i, kline := range klines {
		if kline == nil {
			continue
		}
		k := *kline
		cloned[i] = &k
	}
	return cloned
}
