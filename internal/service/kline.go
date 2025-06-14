package service

import (
	"binance-proxy/internal/tool"
	"container/list"
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	spot "github.com/adshao/go-binance/v2"
	futures "github.com/adshao/go-binance/v2/futures"
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
		for d := tool.NewDelayIterator(); ; d.Delay() {
			s.rw.Lock()
			s.klinesList = nil
			s.rw.Unlock()

			doneC, stopC, err := s.connect()
			if err != nil {
				log.Errorf("%s %s@%s kline websocket connection error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
				continue
			}

			log.Debugf("%s %s@%s kline websocket connected.", s.si.Class, s.si.Symbol, s.si.Interval)
			select {
			case <-s.ctx.Done():
				stopC <- struct{}{}
				return
			case <-doneC:
			}
			log.Warnf("%s %s@%s kline websocket disconnected, trying to reconnect.", s.si.Class, s.si.Symbol, s.si.Interval)
		}
	}()
}

func (s *KlinesSrv) Stop() {
	s.cancel()
}

func (s *KlinesSrv) errHandler(err error) {
	if strings.Contains(err.Error(), "context canceled") {
		log.Warnf("%s %s@%s kline websocket context canceled, will restart connection.", s.si.Class, s.si.Symbol, s.si.Interval)
	} else {
		log.Errorf("%s %s@%s kline websocket connection error: %s connected.", s.si.Class, s.si.Symbol, s.si.Interval, err)
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
		s.klinesList = list.New()
		defer s.initDone()
		return
	}

	var klines interface{}
	var err error
	log.Debugf("%s %s@%s kline initialization through REST.", s.si.Class, s.si.Symbol, s.si.Interval)
	for d := tool.NewDelayIterator(); ; d.Delay() {
		// Check ban status before each attempt
		if banDetector.IsBanned(s.si.Class) {
			log.Debugf("%s %s@%s kline initialization stopped due to API ban", s.si.Class, s.si.Symbol, s.si.Interval)
			s.klinesList = list.New()
			defer s.initDone()
			return
		}

		var resp *http.Response
		if s.si.Class == SPOT {
			RateWait(s.ctx, s.si.Class, http.MethodGet, "/api/v3/klines", url.Values{
				"limit": []string{"1000"},
			})
			client := spot.NewClient("", "")
			klines, err = client.NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(1000).
				Do(s.ctx)
		} else {
			RateWait(s.ctx, s.si.Class, http.MethodGet, "/fapi/v1/klines", url.Values{
				"limit": []string{"1000"},
			})
			client := futures.NewClient("", "")
			klines, err = client.NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(1000).
				Do(s.ctx)
		}

		// Check for bans (resp might be nil for SDK calls, so we check err)
		if banDetector.CheckResponse(s.si.Class, resp, err) {
			log.Debugf("%s %s@%s kline initialization stopped due to detected ban", s.si.Class, s.si.Symbol, s.si.Interval)
			s.klinesList = list.New()
			defer s.initDone()
			return
		}

		if err != nil {
			log.Errorf("%s %s@%s kline initialization via REST failed, error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
			continue
		}

		s.klinesList = list.New()

		if vi, ok := klines.([]*spot.Kline); ok {
			for _, v := range vi {
				t := &Kline{
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

				s.klinesList.PushBack(t)
			}
		} else if vi, ok := klines.([]*futures.Kline); ok {
			for _, v := range vi {
				t := &Kline{
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

				s.klinesList.PushBack(t)
			}
		}

		defer s.initDone()
		break
	}
}

func (s *KlinesSrv) wsHandler(event interface{}) {
	if s.klinesList == nil {
		s.initKlineData()
	}

	// Merge kline
	var k *Kline
	if vi, ok := event.(*spot.WsKlineEvent); ok {
		k = &Kline{
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
		}
	} else if vi, ok := event.(*futures.WsKlineEvent); ok {
		k = &Kline{
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
		}
	}

	log.Tracef("%s %s@%s kline websocket message received for open timestamp %d", s.si.Class, s.si.Symbol, s.si.Interval, k.OpenTime)

	if s.klinesList.Back().Value.(*Kline).OpenTime < k.OpenTime {
		s.klinesList.PushBack(k)
	} else if s.klinesList.Back().Value.(*Kline).OpenTime == k.OpenTime {
		s.klinesList.Back().Value = k
	}

	for s.klinesList.Len() > 1000 {
		s.klinesList.Remove(s.klinesList.Front())
	}

	klinesArr := make([]*Kline, s.klinesList.Len())
	i := 0
	for elems := s.klinesList.Front(); elems != nil; elems = elems.Next() {
		klinesArr[i] = elems.Value.(*Kline)
		i++
	}

	s.rw.Lock()
	defer s.rw.Unlock()

	s.klinesArr = klinesArr
}

func (s *KlinesSrv) GetKlines() []*Kline {
	<-s.initCtx.Done()
	s.rw.RLock()
	defer s.rw.RUnlock()

	return s.klinesArr
}
