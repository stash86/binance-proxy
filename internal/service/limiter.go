package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"golang.org/x/time/rate"
)

const (
	spotWeightLimitPerMinute    = 1200
	futuresWeightLimitPerMinute = 2400
)

var (
	SpotLimiter    = rate.NewLimiter(rate.Limit(spotWeightLimitPerMinute/60), spotWeightLimitPerMinute)
	FuturesLimiter = rate.NewLimiter(rate.Limit(futuresWeightLimitPerMinute/60), futuresWeightLimitPerMinute)
)

func RateWait(ctx context.Context, class Class, method, path string, query url.Values) error {
	weight := RequestWeight(class, method, path, query)
	if class == SPOT {
		return SpotLimiter.WaitN(ctx, weight)
	}
	return FuturesLimiter.WaitN(ctx, weight)
}

func RequestWeight(class Class, method, path string, query url.Values) int {
	switch path {
	case "/api/v3/klines":
		return 2
	case "/fapi/v1/klines":
		return futuresKlinesWeight(limit(query, 500))
	case "/api/v3/depth":
		return spotDepthWeight(limit(query, 100))
	case "/fapi/v1/depth":
		return futuresDepthWeight(limit(query, 500))
	case "/api/v3/ticker/24hr", "/fapi/v1/ticker/24hr":
		if class == SPOT {
			return spotTicker24hrWeight(query)
		}
		return futuresTicker24hrWeight(query)
	case "/api/v3/exchangeInfo":
		return 20
	case "/fapi/v1/exchangeInfo":
		return 1
	case "/api/v3/account", "/api/v3/myTrades":
		return 10
	case "/api/v3/order":
		if method == http.MethodGet {
			return 2
		}
	case "/fapi/v1/userTrades", "/fapi/v2/account":
		return 5
	}

	return 1
}

func limit(query url.Values, defaultLimit int) int {
	if query == nil {
		return defaultLimit
	}

	limitInt, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limitInt <= 0 {
		return defaultLimit
	}
	return limitInt
}

func spotDepthWeight(limit int) int {
	switch {
	case limit <= 100:
		return 5
	case limit <= 500:
		return 25
	case limit <= 1000:
		return 50
	default:
		return 250
	}
}

func futuresDepthWeight(limit int) int {
	switch {
	case limit <= 50:
		return 2
	case limit <= 100:
		return 5
	case limit <= 500:
		return 10
	default:
		return 20
	}
}

func futuresKlinesWeight(limit int) int {
	switch {
	case limit < 100:
		return 1
	case limit < 500:
		return 2
	case limit <= 1000:
		return 5
	default:
		return 10
	}
}

func spotTicker24hrWeight(query url.Values) int {
	if query == nil {
		return 80
	}

	if query.Get("symbol") != "" {
		return 2
	}

	symbols := query.Get("symbols")
	if symbols == "" {
		return 80
	}

	count, ok := symbolsCount(symbols)
	if !ok {
		return 80
	}
	switch {
	case count <= 20:
		return 2
	case count <= 100:
		return 40
	default:
		return 80
	}
}

func futuresTicker24hrWeight(query url.Values) int {
	if query != nil && query.Get("symbol") != "" {
		return 1
	}
	return 40
}

func symbolsCount(symbols string) (int, bool) {
	var parsed []string
	if err := json.Unmarshal([]byte(symbols), &parsed); err != nil {
		return 0, false
	}
	return len(parsed), true
}
