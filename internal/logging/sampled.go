package logging

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SampledLogger reduces log volume by sampling repeated messages
type SampledLogger struct {
	logger     *RateLimitedLogger
	cache      map[string]*logEntry
	mu         sync.RWMutex
	maxEntries int
	cleanupInterval time.Duration
	lastCleanup     time.Time
}

type logEntry struct {
	count      int64
	firstSeen  time.Time
	lastSeen   time.Time
	level      logrus.Level
	message    string
	suppressed bool
}

// NewSampledLogger creates a new sampled logger
func NewSampledLogger(rateLimitedLogger *RateLimitedLogger) *SampledLogger {
	return &SampledLogger{
		logger:          rateLimitedLogger,
		cache:           make(map[string]*logEntry),
		maxEntries:      1000,
		cleanupInterval: 5 * time.Minute,
		lastCleanup:     time.Now(),
	}
}

// shouldLog determines if a message should be logged based on sampling rules
func (sl *SampledLogger) shouldLog(level logrus.Level, message string) bool {
	// Always log errors and warnings
	if level <= logrus.WarnLevel {
		return true
	}
	
	sl.mu.Lock()
	defer sl.mu.Unlock()
	
	// Cleanup old entries periodically
	if time.Since(sl.lastCleanup) > sl.cleanupInterval {
		sl.cleanup()
	}
	
	key := fmt.Sprintf("%s:%s", level.String(), message)
	entry, exists := sl.cache[key]
	
	if !exists {
		// First time seeing this message
		sl.cache[key] = &logEntry{
			count:     1,
			firstSeen: time.Now(),
			lastSeen:  time.Now(),
			level:     level,
			message:   message,
		}
		return true
	}
	
	entry.count++
	entry.lastSeen = time.Now()
	
	// Sampling rules:
	// 1. Log first occurrence
	// 2. Log every 10th occurrence for debug messages
	// 3. Log every 100th occurrence for trace messages
	// 4. Log once per minute for repeated messages
	
	timeSinceFirst := time.Since(entry.firstSeen)
	
	switch level {
	case logrus.DebugLevel:
		// Log every 10th occurrence or once per minute
		if entry.count%10 == 0 || timeSinceFirst > time.Minute {
			return true
		}
	case logrus.TraceLevel:
		// Log every 100th occurrence or once per 5 minutes
		if entry.count%100 == 0 || timeSinceFirst > 5*time.Minute {
			return true
		}
	}
	
	return false
}

// cleanup removes old entries to prevent memory leaks
func (sl *SampledLogger) cleanup() {
	now := time.Now()
	cutoff := now.Add(-10 * time.Minute) // Remove entries older than 10 minutes
	
	for key, entry := range sl.cache {
		if entry.lastSeen.Before(cutoff) {
			// Log a summary if the message was suppressed
			if entry.count > 1 {
				sl.logger.Infof("Log message suppressed: '%s' occurred %d times in %v",
					entry.message, entry.count, entry.lastSeen.Sub(entry.firstSeen))
			}
			delete(sl.cache, key)
		}
	}
	
	// If still too many entries, remove oldest
	if len(sl.cache) > sl.maxEntries {
		// Simple cleanup: remove half of the entries
		count := 0
		target := len(sl.cache) / 2
		for key := range sl.cache {
			if count >= target {
				break
			}
			delete(sl.cache, key)
			count++
		}
	}
	
	sl.lastCleanup = now
}

// Sampled logging methods
func (sl *SampledLogger) Error(args ...interface{}) {
	message := fmt.Sprint(args...)
	if sl.shouldLog(logrus.ErrorLevel, message) {
		sl.logger.Error(args...)
	}
}

func (sl *SampledLogger) Errorf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if sl.shouldLog(logrus.ErrorLevel, message) {
		sl.logger.Errorf(format, args...)
	}
}

func (sl *SampledLogger) Warn(args ...interface{}) {
	message := fmt.Sprint(args...)
	if sl.shouldLog(logrus.WarnLevel, message) {
		sl.logger.Warn(args...)
	}
}

func (sl *SampledLogger) Warnf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if sl.shouldLog(logrus.WarnLevel, message) {
		sl.logger.Warnf(format, args...)
	}
}

func (sl *SampledLogger) Info(args ...interface{}) {
	message := fmt.Sprint(args...)
	if sl.shouldLog(logrus.InfoLevel, message) {
		sl.logger.Info(args...)
	}
}

func (sl *SampledLogger) Infof(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if sl.shouldLog(logrus.InfoLevel, message) {
		sl.logger.Infof(format, args...)
	}
}

func (sl *SampledLogger) Debug(args ...interface{}) {
	message := fmt.Sprint(args...)
	if sl.shouldLog(logrus.DebugLevel, message) {
		sl.logger.Debug(args...)
	}
}

func (sl *SampledLogger) Debugf(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if sl.shouldLog(logrus.DebugLevel, message) {
		sl.logger.Debugf(format, args...)
	}
}

func (sl *SampledLogger) Trace(args ...interface{}) {
	message := fmt.Sprint(args...)
	if sl.shouldLog(logrus.TraceLevel, message) {
		sl.logger.Trace(args...)
	}
}

func (sl *SampledLogger) Tracef(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if sl.shouldLog(logrus.TraceLevel, message) {
		sl.logger.Tracef(format, args...)
	}
}

// GetSamplingStats returns statistics about log sampling
func (sl *SampledLogger) GetSamplingStats() map[string]interface{} {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	
	stats := map[string]interface{}{
		"cached_messages": len(sl.cache),
		"max_entries":     sl.maxEntries,
		"last_cleanup":    sl.lastCleanup,
	}
	
	// Count suppressed messages by level
	suppressedByLevel := make(map[string]int)
	totalSuppressed := 0
	
	for _, entry := range sl.cache {
		if entry.count > 1 {
			level := entry.level.String()
			suppressedByLevel[level] += int(entry.count - 1)
			totalSuppressed += int(entry.count - 1)
		}
	}
	
	stats["suppressed_by_level"] = suppressedByLevel
	stats["total_suppressed"] = totalSuppressed
	
	return stats
}

// Force flush remaining suppressed message summaries
func (sl *SampledLogger) Flush() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	
	for _, entry := range sl.cache {
		if entry.count > 1 {
			sl.logger.Infof("Final log summary: '%s' occurred %d times in %v",
				entry.message, entry.count, entry.lastSeen.Sub(entry.firstSeen))
		}
	}
	
	sl.cache = make(map[string]*logEntry)
}
