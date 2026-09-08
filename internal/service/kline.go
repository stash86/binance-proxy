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
	klineHistoryLimit       = 1000
	klineInitTimeout        = 2 * time.Second
	klineFreshnessTimeout   = 15 * time.Second
	klineBoundaryGrace      = 5 * time.Second
	klineStallTimeout       = 30 * time.Second
	klineWatchInterval      = time.Second
	klineHealthyResetPeriod = time.Minute
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
	IsFinal                  bool `json:"-"`
}

// IsCurrent checks candle coverage, independently of feed freshness. The HTTP
// handler rechecks it because a candle can close after the cache was copied.
func (k *Kline) IsCurrent(nowMS int64) bool {
	return k != nil && k.CloseTime >= nowMS-klineBoundaryGrace.Milliseconds() &&
		(nowMS <= k.CloseTime || k.IsFinal)
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
	lastUpdate time.Time
	lastEvent  int64
	historyAt  time.Time
	recoveryAt time.Time
}

func NewKlinesSrv(ctx context.Context, si *symbolInterval) *KlinesSrv {
	s := &KlinesSrv{si: si}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.initCtx, s.initDone = context.WithCancel(context.Background())

	return s
}

func (s *KlinesSrv) Start() {
	go s.run(s.connect, func(ctx context.Context) error {
		return waitKlineConnection(ctx, s.si.Class)
	})
}

// Waiting for doneC after cancellation also joins the SDK's synchronous callback.
// A replacement connection must never share the cache with an old callback.
func (s *KlinesSrv) run(connect func(context.Context) (chan struct{}, chan struct{}, error), waitConnection func(context.Context) error) {
	delay := newKlineRetryDelay()
	defer s.clearKlineList()
	for s.ctx.Err() == nil {
		if err := waitConnection(s.ctx); err != nil {
			return
		}
		connectionCtx, cancel := context.WithCancel(s.ctx)
		doneC, stopC, err := connect(connectionCtx)
		if err != nil {
			cancel()
			log.Errorf("%s %s@%s kline websocket connection error: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
		} else {
			log.Debugf("%s %s@%s kline websocket connected.", s.si.Class, s.si.Symbol, s.si.Interval)
			healthy := s.watchConnection(doneC, time.Now())
			cancel()
			s.clearKlineList()
			close(stopC)
			<-doneC
			if healthy {
				delay.Reset()
			}
		}
		if !delay.DelayContextWithJitter(s.ctx) {
			return
		}
	}
}

func newKlineRetryDelay() *tool.DelayIterator {
	delay := tool.NewDelayIterator()
	delay.SetDelayList([]time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute})
	return delay
}

func (s *KlinesSrv) watchConnection(doneC <-chan struct{}, started time.Time) bool {
	ticker := time.NewTicker(klineWatchInterval)
	defer ticker.Stop()
	var healthySince time.Time
	healthy := false
	for {
		select {
		case <-s.ctx.Done():
			return healthy
		case <-doneC:
			log.Warnf("%s %s@%s kline websocket disconnected; rebuilding candle cache.", s.si.Class, s.si.Symbol, s.si.Interval)
			return healthy
		case now := <-ticker.C:
			s.rw.RLock()
			lastUpdate := s.lastUpdate
			recoveryAt := s.recoveryAt
			fresh := s.freshLocked(now)
			s.rw.RUnlock()
			if fresh {
				if healthySince.IsZero() {
					healthySince = now
				}
				healthy = healthy || now.Sub(healthySince) >= klineHealthyResetPeriod
			} else {
				healthySince = time.Time{}
			}
			if lastUpdate.IsZero() {
				lastUpdate = started
				if recoveryAt.After(started) {
					lastUpdate = recoveryAt
				}
			}
			if now.Sub(lastUpdate) >= klineStallTimeout {
				log.Warnf("%s %s@%s kline websocket has no fresh data for %s; reconnecting.", s.si.Class, s.si.Symbol, s.si.Interval, klineStallTimeout)
				return healthy
			}
		}
	}
}

func (s *KlinesSrv) Stop() {
	s.cancel()
	s.clearKlineList()
}

func (s *KlinesSrv) errHandler(err error) {
	if err == nil {
		return
	}
	s.clearKlineList()

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

func (s *KlinesSrv) connect(ctx context.Context) (doneC, stopC chan struct{}, err error) {
	if s.si.Class == SPOT {
		return spot.WsKlineServe(s.si.Symbol,
			s.si.Interval,
			func(event *spot.WsKlineEvent) { s.wsHandler(ctx, event) },
			s.errHandler,
		)
	} else {
		return futures.WsKlineServe(s.si.Symbol,
			s.si.Interval,
			func(event *futures.WsKlineEvent) { s.wsHandler(ctx, event) },
			s.errHandler,
		)
	}
}

// Bootstrap is scoped to one connection, including its REST retries. The first
// event only triggers bootstrap; it must not overwrite a newer REST snapshot.
func (s *KlinesSrv) initKlineData(ctx context.Context) {
	banDetector := GetBanDetector()
	for delay := newKlineRetryDelay(); ctx.Err() == nil; {
		if banDetector.IsBanned(s.si.Class) {
			return
		}
		path := "/api/v3/klines"
		if s.si.Class == FUTURES {
			path = "/fapi/v1/klines"
		}
		if err := RateWait(ctx, s.si.Class, http.MethodGet, path, url.Values{"limit": {"1000"}}); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}

		log.Debugf("%s %s@%s kline initialization through REST.", s.si.Class, s.si.Symbol, s.si.Interval)
		fetchStarted := time.Now()
		klinesList := list.New()
		var err error
		if s.si.Class == SPOT {
			var klines []*spot.Kline
			klines, err = spot.NewClient("", "").NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(klineHistoryLimit).Do(ctx)
			for _, k := range klines {
				if k != nil {
					klinesList.PushBack(klineFromSpotREST(k))
				}
			}
		} else {
			var klines []*futures.Kline
			klines, err = futures.NewClient("", "").NewKlinesService().
				Symbol(s.si.Symbol).Interval(s.si.Interval).Limit(klineHistoryLimit).Do(ctx)
			for _, k := range klines {
				if k != nil {
					klinesList.PushBack(klineFromFuturesREST(k))
				}
			}
		}
		if ctx.Err() != nil || banDetector.CheckResponse(s.si.Class, nil, err) {
			return
		}
		if err != nil {
			log.Errorf("%s %s@%s kline initialization via REST failed: %s.", s.si.Class, s.si.Symbol, s.si.Interval, err)
			if !delay.DelayContextWithJitter(ctx) {
				return
			}
			continue
		}
		if klinesList.Len() == 0 {
			return
		}
		for item := klinesList.Front(); item != nil; item = item.Next() {
			k := item.Value.(*Kline)
			k.IsFinal = k.CloseTime < fetchStarted.UnixMilli()
		}
		s.rw.Lock()
		if ctx.Err() == nil {
			s.klinesList = klinesList
			s.klinesArr = klinesFromList(klinesList)
			s.historyAt = time.Now()
		}
		s.rw.Unlock()
		return
	}
}

func (s *KlinesSrv) wsHandler(ctx context.Context, event interface{}) {
	receivedAt := time.Now()
	k, eventTime, ok := klineFromWSEvent(event)
	if !ok || ctx.Err() != nil || eventTime <= 0 ||
		eventTime < receivedAt.Add(-klineFreshnessTimeout).UnixMilli() ||
		eventTime > receivedAt.Add(klineBoundaryGrace).UnixMilli() ||
		k.OpenTime > receivedAt.Add(klineBoundaryGrace).UnixMilli() ||
		k.CloseTime < k.OpenTime || k.CloseTime < receivedAt.Add(-klineBoundaryGrace).UnixMilli() {
		return
	}

	s.rw.Lock()
	if ctx.Err() != nil {
		s.rw.Unlock()
		return
	}
	if s.klinesList == nil {
		s.rw.Unlock()
		s.initKlineData(ctx)
		return
	}
	// Buffered events predating bootstrap or the last accepted event must not
	// regress the snapshot or renew its freshness.
	if eventTime <= s.historyAt.UnixMilli() || eventTime <= s.lastEvent {
		s.rw.Unlock()
		return
	}
	if s.klinesList.Len() > 0 {
		last := s.klinesList.Back().Value.(*Kline)
		if k.OpenTime < last.OpenTime {
			s.rw.Unlock()
			return
		}
		if k.OpenTime > last.OpenTime && (k.OpenTime != last.CloseTime+1 || !last.IsFinal) {
			// Recover a missing candle or missed final update before exposing the
			// next candle. The history download is canceled with this connection.
			s.clearKlineListLocked()
			s.rw.Unlock()
			s.initKlineData(ctx)
			return
		}
		if last.OpenTime == k.OpenTime {
			if last.IsFinal && !k.IsFinal {
				s.rw.Unlock()
				return
			}
			s.klinesList.Back().Value = k
		} else {
			s.klinesList.PushBack(k)
		}
	} else {
		s.klinesList.PushBack(k)
	}
	for s.klinesList.Len() > klineHistoryLimit {
		s.klinesList.Remove(s.klinesList.Front())
	}
	s.klinesArr = klinesFromList(s.klinesList)
	s.lastUpdate = receivedAt
	s.lastEvent = eventTime
	s.initDone()
	s.rw.Unlock()
}

func (s *KlinesSrv) GetKlines() []*Kline {
	if !s.waitForInit() {
		return nil
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	if !s.freshLocked(time.Now()) {
		return nil
	}
	return cloneKlines(s.klinesArr)
}

func (s *KlinesSrv) freshLocked(now time.Time) bool {
	if s.ctx.Err() != nil || s.lastUpdate.IsZero() || len(s.klinesArr) == 0 ||
		now.Sub(s.lastUpdate) > klineFreshnessTimeout ||
		s.lastEvent < now.Add(-klineFreshnessTimeout).UnixMilli() {
		return false
	}
	last := s.klinesArr[len(s.klinesArr)-1]
	return last.IsCurrent(now.UnixMilli())
}

func (s *KlinesSrv) waitForInit() bool {
	if s.ctx.Err() != nil {
		return false
	}
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

	s.clearKlineListLocked()
}

func (s *KlinesSrv) clearKlineListLocked() {
	if s.klinesList != nil || s.recoveryAt.IsZero() {
		s.recoveryAt = time.Now()
	}
	s.klinesList = nil
	s.klinesArr = nil
	s.lastUpdate = time.Time{}
	s.lastEvent = 0
	s.historyAt = time.Time{}
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

func klineFromWSEvent(event interface{}) (*Kline, int64, bool) {
	switch vi := event.(type) {
	case *spot.WsKlineEvent:
		if vi == nil {
			return nil, 0, false
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
			IsFinal:                  vi.Kline.IsFinal,
		}, vi.Time, true
	case *futures.WsKlineEvent:
		if vi == nil {
			return nil, 0, false
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
			IsFinal:                  vi.Kline.IsFinal,
		}, vi.Time, true
	default:
		return nil, 0, false
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
