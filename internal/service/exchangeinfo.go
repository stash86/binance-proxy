package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"binance-proxy/internal/tool"

	log "github.com/sirupsen/logrus"
)

const (
	exchangeInfoRefreshInterval = 60 * time.Second
	exchangeInfoInitTimeout     = 2 * time.Second
)

type ExchangeInfoSrv struct {
	rw sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	initCtx  context.Context
	initDone context.CancelFunc

	refreshDur   time.Duration
	si           *symbolInterval
	exchangeInfo []byte
}

// HTTP client pool for connection reuse
var (
	httpClientOnce sync.Once
	httpClient     *http.Client
)

func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			ForceAttemptHTTP2:   true,
		}

		httpClient = &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
	})
	return httpClient
}

func NewExchangeInfoSrv(ctx context.Context, si *symbolInterval) *ExchangeInfoSrv {
	s := &ExchangeInfoSrv{
		si:         si,
		refreshDur: exchangeInfoRefreshInterval,
	}
	log.Tracef("%s exchangeInfo initialization with refresh of %.0fs.", s.si.Class, s.refreshDur.Seconds())
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.initCtx, s.initDone = context.WithCancel(context.Background())

	return s
}

func (s *ExchangeInfoSrv) Start() {
	go func() {
		s.retryRefreshExchangeInfo()

		ticker := time.NewTicker(s.refreshDur)
		defer ticker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}

			s.retryRefreshExchangeInfo()
		}
	}()
}

func (s *ExchangeInfoSrv) Stop() {
	s.cancel()
}

func (s *ExchangeInfoSrv) GetExchangeInfo() []byte {
	if !s.waitForInit() {
		return nil
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	return append([]byte(nil), s.exchangeInfo...)
}

func (s *ExchangeInfoSrv) waitForInit() bool {
	select {
	case <-s.initCtx.Done():
		return true
	default:
	}

	timer := time.NewTimer(exchangeInfoInitTimeout)
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

func (s *ExchangeInfoSrv) retryRefreshExchangeInfo() {
	for d := tool.NewDelayIterator(); ; {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		if s.refreshExchangeInfo() == nil {
			return
		}

		if !d.DelayContext(s.ctx) {
			return
		}
	}
}

func (s *ExchangeInfoSrv) refreshExchangeInfo() error {
	// Check if API is banned
	banDetector := GetBanDetector()
	if banDetector.IsBanned(s.si.Class) {
		log.Debugf("%s exchangeInfo refresh skipped due to API ban", s.si.Class)
		return nil // Don't retry during ban
	}

	var url string
	if s.si.Class == SPOT {
		url = "https://api.binance.com/api/v3/exchangeInfo"
		if err := RateWait(s.ctx, s.si.Class, http.MethodGet, "/api/v3/exchangeInfo", nil); err != nil {
			return err
		}
	} else {
		url = "https://fapi.binance.com/fapi/v1/exchangeInfo"
		if err := RateWait(s.ctx, s.si.Class, http.MethodGet, "/fapi/v1/exchangeInfo", nil); err != nil {
			return err
		}
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}

	// Use pooled HTTP client instead of http.Get()
	client := getHTTPClient()
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Errorf("%s exchangeInfo request creation failed, error: %s.", s.si.Class, err)
		return err
	}

	resp, err := client.Do(req)

	// Check for bans
	if banDetector.CheckResponse(s.si.Class, resp, err) {
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}

	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		log.Errorf("%s exchangeInfo refresh failed, error: %s.", s.si.Class, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := fmt.Errorf("%s exchangeInfo refresh failed with upstream status %s", s.si.Class, resp.Status)
		log.Error(err)
		return err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("%s exchangeInfo response read failed, error: %s.", s.si.Class, err)
		return err
	}

	s.rw.Lock()
	firstLoad := s.exchangeInfo == nil
	s.exchangeInfo = data
	s.rw.Unlock()

	if firstLoad {
		s.initDone()
	}

	log.Debugf("%s exchangeInfo refreshed successfully.", s.si.Class)

	return nil
}
