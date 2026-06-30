package logcache

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	msg := `request "BTCUSDT" failed at 2026-06-30T10:11:12Z with code 429 weight 12.5`

	got := Normalize(msg)
	want := "request failed at with code weight"
	if got != want {
		t.Fatalf("Normalize() = %q, want %q", got, want)
	}
}

func TestLogOncePerDurationSuppressesNormalizedDuplicates(t *testing.T) {
	resetLogCacheState(t)

	var logs []string
	SetLoggerHook(func(level, msg string) {
		logs = append(logs, level+":"+msg)
	})

	LogOncePerDuration("warn", "proxy transport error: 502")
	LogOncePerDuration("warn", "proxy transport error: 503")

	want := []string{"warn:proxy transport error: 502"}
	if !reflect.DeepEqual(logs, want) {
		t.Fatalf("logs = %#v, want %#v", logs, want)
	}
}

func TestShouldLogAllowsAfterSuppressDuration(t *testing.T) {
	resetLogCacheState(t)

	SuppressDuration = time.Minute
	now := time.Unix(1000, 0)

	if !shouldLog("same-key", now) {
		t.Fatal("first log was suppressed")
	}
	if shouldLog("same-key", now.Add(30*time.Second)) {
		t.Fatal("duplicate inside suppress duration was allowed")
	}
	if !shouldLog("same-key", now.Add(time.Minute)) {
		t.Fatal("duplicate at suppress duration boundary was suppressed")
	}
}

func TestShouldLogBoundsCache(t *testing.T) {
	resetLogCacheState(t)

	SuppressDuration = time.Hour
	maxCacheEntries = 2
	now := time.Unix(1000, 0)

	cacheLock.Lock()
	cache["oldest"] = now.Add(-3 * time.Second)
	cache["middle"] = now.Add(-2 * time.Second)
	cacheLock.Unlock()

	if !shouldLog("newest", now) {
		t.Fatal("new key was suppressed")
	}

	cacheLock.Lock()
	defer cacheLock.Unlock()
	if len(cache) > maxCacheEntries {
		t.Fatalf("cache len = %d, want <= %d", len(cache), maxCacheEntries)
	}
	if _, ok := cache["oldest"]; ok {
		t.Fatal("oldest cache entry was not pruned")
	}
	if _, ok := cache["newest"]; !ok {
		t.Fatal("new cache entry missing")
	}
}

func TestLogOncePerDurationHookCanReenter(t *testing.T) {
	resetLogCacheState(t)

	var mu sync.Mutex
	var logs []string
	SetLoggerHook(func(level, msg string) {
		mu.Lock()
		logs = append(logs, msg)
		mu.Unlock()

		if msg == "outer" {
			LogOncePerDuration("info", "inner")
		}
	})

	LogOncePerDuration("info", "outer")

	want := []string{"outer", "inner"}
	if !reflect.DeepEqual(logs, want) {
		t.Fatalf("logs = %#v, want %#v", logs, want)
	}
}

func TestSuppressingWriterSuppressesDuplicates(t *testing.T) {
	resetLogCacheState(t)

	var next bytes.Buffer
	writer := NewSuppressingWriter(&next)

	n, err := writer.Write([]byte("http transport error 502\n"))
	if err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if n != len("http transport error 502\n") {
		t.Fatalf("first Write() n = %d, want full length", n)
	}

	n, err = writer.Write([]byte("http transport error 503\n"))
	if err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if n != len("http transport error 503\n") {
		t.Fatalf("second Write() n = %d, want full length", n)
	}

	want := "http transport error 502\n"
	if got := next.String(); got != want {
		t.Fatalf("written = %q, want %q", got, want)
	}
}

func TestSuppressingWriterUsesHookInsteadOfNextWriter(t *testing.T) {
	resetLogCacheState(t)

	var hooked []string
	SetWriterHook(func(msg string) {
		hooked = append(hooked, msg)
	})

	var next bytes.Buffer
	writer := NewSuppressingWriter(&next)

	n, err := writer.Write([]byte("http: panic serving request\n"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len("http: panic serving request\n") {
		t.Fatalf("Write() n = %d, want full length", n)
	}
	if got := next.String(); got != "" {
		t.Fatalf("next writer received %q, want empty", got)
	}

	want := []string{"http: panic serving request\n"}
	if !reflect.DeepEqual(hooked, want) {
		t.Fatalf("hooked = %#v, want %#v", hooked, want)
	}
}

func TestSuppressingWriterReturnsNextWriterError(t *testing.T) {
	resetLogCacheState(t)

	wantErr := errors.New("write failed")
	writer := NewSuppressingWriter(errorWriter{err: wantErr})

	n, err := writer.Write([]byte("http: failed\n"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write() error = %v, want %v", err, wantErr)
	}
	if n != 0 {
		t.Fatalf("Write() n = %d, want 0", n)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func resetLogCacheState(t *testing.T) {
	t.Helper()

	cacheLock.Lock()
	oldCache := cache
	oldSuppressDuration := SuppressDuration
	oldMaxCacheEntries := maxCacheEntries
	cache = make(map[string]time.Time)
	SuppressDuration = 2 * time.Minute
	maxCacheEntries = 4096
	cacheLock.Unlock()

	hookLock.Lock()
	oldLoggerHook := loggerHook
	oldWriterHook := writerHook
	loggerHook = nil
	writerHook = nil
	hookLock.Unlock()

	t.Cleanup(func() {
		cacheLock.Lock()
		cache = oldCache
		SuppressDuration = oldSuppressDuration
		maxCacheEntries = oldMaxCacheEntries
		cacheLock.Unlock()

		hookLock.Lock()
		loggerHook = oldLoggerHook
		writerHook = oldWriterHook
		hookLock.Unlock()
	})
}
