package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type trackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (rc *trackingReadCloser) Close() error {
	rc.closed = true
	return nil
}

func TestCheckResponseUsesFirstWeightHeader(t *testing.T) {
	bd := &BanDetector{}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}
	resp.Header.Set("X-MBX-USED-WEIGHT-1M", "1100")

	if !bd.CheckResponse(SPOT, resp, nil) {
		t.Fatal("expected high first-response weight header to trigger throttling")
	}

	used, limit, resetTime := bd.GetWeightInfo(SPOT)
	if used != 1100 {
		t.Fatalf("weight used = %d, want 1100", used)
	}
	if limit != defaultSpotWeightLimit {
		t.Fatalf("weight limit = %d, want %d", limit, defaultSpotWeightLimit)
	}
	if resetTime.IsZero() {
		t.Fatal("expected reset time to be set")
	}
}

func TestCheckResponseExplicitBanTakesPrecedenceOverWeightLimit(t *testing.T) {
	bd := &BanDetector{}
	resp := &http.Response{
		StatusCode: http.StatusTeapot,
		Header:     make(http.Header),
	}
	resp.Header.Set("X-MBX-USED-WEIGHT-1M", "1199")
	resp.Header.Set("Retry-After", "600")

	if !bd.CheckResponse(SPOT, resp, nil) {
		t.Fatal("expected explicit ban response to trigger ban")
	}

	banned, until := bd.GetBanStatus(SPOT)
	if !banned {
		t.Fatal("expected spot to be banned")
	}
	remaining := time.Until(until)
	if remaining < 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("ban duration = %v, want close to 10 minutes", remaining)
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	bd := &BanDetector{}
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	want := now.Add(2 * time.Minute)
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Retry-After", want.Format(http.TimeFormat))

	got := bd.parseRetryAfter(resp, now)
	if !got.Equal(want) {
		t.Fatalf("retry-after = %v, want %v", got, want)
	}
}

func TestParseBanExpiryRestoresBodyAndClosesOriginal(t *testing.T) {
	bd := &BanDetector{}
	body := `{"code":-1003,"msg":"IP banned until 1782813600000"}`
	originalBody := &trackingReadCloser{Reader: strings.NewReader(body)}
	resp := &http.Response{Body: originalBody}

	got := bd.parseBanExpiryNonDestructive(resp)
	if got.Unix() != 1782813600 {
		t.Fatalf("ban expiry unix = %d, want 1782813600", got.Unix())
	}
	if !originalBody.closed {
		t.Fatal("expected original body to be closed")
	}
	restoredBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restoredBody) != body {
		t.Fatalf("restored body = %q, want %q", string(restoredBody), body)
	}
}

func TestGetBanStatusExpiresStaleBan(t *testing.T) {
	bd := &BanDetector{
		spotBanned:       true,
		spotRecoveryTime: time.Now().Add(-time.Second),
	}

	banned, _ := bd.GetBanStatus(SPOT)
	if banned {
		t.Fatal("expected stale ban to be cleared")
	}
	if bd.spotBanned {
		t.Fatal("expected detector state to clear stale ban")
	}
}
