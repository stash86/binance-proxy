package logcache

import (
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	cache            = make(map[string]time.Time)
	cacheLock        sync.Mutex
	SuppressDuration = 2 * time.Minute

	maxCacheEntries = 4096

	numberRegexp    = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)
	timestampRegexp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	quotedRegexp    = regexp.MustCompile(`"[^"]*"`)

	// Optional hooks for unified logging backends
	hookLock   sync.RWMutex
	loggerHook func(level, msg string)
	writerHook func(msg string)
)

func Normalize(msg string) string {
	msg = quotedRegexp.ReplaceAllString(msg, "")
	msg = timestampRegexp.ReplaceAllString(msg, "")
	msg = numberRegexp.ReplaceAllString(msg, "")
	msg = strings.Join(strings.Fields(msg), " ")
	return msg
}

func LogOncePerDuration(level, msg string) {
	key := Normalize(msg)
	if !shouldLog(key, time.Now()) {
		return
	}

	if hook := getLoggerHook(); hook != nil {
		hook(level, msg)
		return
	}

	logByLevel(level, msg)
}

func shouldLog(key string, now time.Time) bool {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	duration := SuppressDuration
	last, found := cache[key]
	if found && duration > 0 && now.Sub(last) < duration {
		return false
	}

	cache[key] = now
	cleanupCacheLocked(now, duration)
	return true
}

func cleanupCacheLocked(now time.Time, duration time.Duration) {
	if len(cache) <= maxCacheEntries {
		return
	}

	if duration > 0 {
		for key, last := range cache {
			if now.Sub(last) >= duration {
				delete(cache, key)
			}
		}
	}
	for len(cache) > maxCacheEntries {
		deleteOldestCacheEntryLocked()
	}
}

func deleteOldestCacheEntryLocked() {
	var oldestKey string
	var oldestTime time.Time
	found := false
	for key, last := range cache {
		if !found || last.Before(oldestTime) {
			oldestKey = key
			oldestTime = last
			found = true
		}
	}
	if found {
		delete(cache, oldestKey)
	}
}

func getLoggerHook() func(level, msg string) {
	hookLock.RLock()
	defer hookLock.RUnlock()

	return loggerHook
}

func getWriterHook() func(msg string) {
	hookLock.RLock()
	defer hookLock.RUnlock()

	return writerHook
}

func logByLevel(level, msg string) {
	switch level {
	case "warn":
		log.Printf("WARN: %s", msg)
	case "info":
		log.Printf("INFO: %s", msg)
	case "error":
		log.Printf("ERROR: %s", msg)
	default:
		log.Print(msg)
	}
}

// suppressingWriter wraps an io.Writer and suppresses repeated/similar lines
// within SuppressDuration using the same normalization as above.
type suppressingWriter struct {
	next io.Writer
}

// NewSuppressingWriter returns an io.Writer suitable for net/http Server.ErrorLog.SetOutput.
func NewSuppressingWriter(next io.Writer) io.Writer {
	return &suppressingWriter{next: next}
}

func (w *suppressingWriter) Write(p []byte) (int, error) {
	msg := string(p)
	key := Normalize(msg)
	if !shouldLog(key, time.Now()) {
		// Pretend we wrote it to avoid backpressure; drop the line.
		return len(p), nil
	}

	if hook := getWriterHook(); hook != nil {
		hook(msg)
		return len(p), nil
	}
	if w.next != nil {
		return w.next.Write(p)
	}
	// Nothing to write to but not an error; pretend success
	return len(p), nil
}

// SetLoggerHook sets a custom hook to handle LogOncePerDuration output.
// The hook receives a level (e.g., "info", "warn", "error") and the message.
func SetLoggerHook(hook func(level, msg string)) {
	hookLock.Lock()
	defer hookLock.Unlock()

	loggerHook = hook
}

// SetWriterHook sets a custom hook to handle writes from the suppressing writer.
// Useful to route net/http Server.ErrorLog output into a different logging backend.
func SetWriterHook(hook func(msg string)) {
	hookLock.Lock()
	defer hookLock.Unlock()

	writerHook = hook
}
