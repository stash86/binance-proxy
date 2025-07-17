package logcache

import (
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

	numberRegexp    = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)
	timestampRegexp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	quotedRegexp    = regexp.MustCompile(`"[^"]*"`)
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
	cacheLock.Lock()
	defer cacheLock.Unlock()
	last, found := cache[key]
	if found && time.Since(last) < SuppressDuration {
		return
	}
	cache[key] = time.Now()
	// Use standard log for demonstration; replace with logrus or other as needed
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
