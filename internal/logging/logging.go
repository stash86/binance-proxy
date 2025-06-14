package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogConfig holds logging configuration with disk management
type LogConfig struct {
	Level          string        `long:"level" env:"LEVEL" description:"Log level" default:"info"`
	Format         string        `long:"format" env:"FORMAT" description:"Log format (text, json)" default:"text"`
	Output         string        `long:"output" env:"OUTPUT" description:"Log output (stdout, stderr, file path)" default:"stdout"`
	EnableStructured bool        `long:"enable-structured" env:"ENABLE_STRUCTURED" description:"Enable structured logging"`
	
	// File rotation settings
	MaxSize        int           `long:"max-size-mb" env:"MAX_SIZE_MB" description:"Maximum log file size in MB" default:"100"`
	MaxBackups     int           `long:"max-backups" env:"MAX_BACKUPS" description:"Maximum number of backup files" default:"5"`
	MaxAge         int           `long:"max-age-days" env:"MAX_AGE_DAYS" description:"Maximum age of log files in days" default:"30"`
	Compress       bool          `long:"compress" env:"COMPRESS" description:"Compress backup log files" default:"true"`
	
	// Rate limiting for verbose logs
	EnableRateLimit bool         `long:"enable-rate-limit" env:"ENABLE_RATE_LIMIT" description:"Enable log rate limiting" default:"false"`
	RateLimit      int           `long:"rate-limit" env:"RATE_LIMIT" description:"Log rate limit per second" default:"100"`
	BurstLimit     int           `long:"burst-limit" env:"BURST_LIMIT" description:"Log burst limit" default:"200"`
	
	// Disk space protection
	MaxDiskUsageMB int           `long:"max-disk-usage-mb" env:"MAX_DISK_USAGE_MB" description:"Maximum disk usage for logs in MB" default:"1000"`
	CleanupInterval time.Duration `long:"cleanup-interval" env:"CLEANUP_INTERVAL" description:"Cleanup check interval" default:"1h"`
}

// RateLimitedLogger wraps logrus with rate limiting
type RateLimitedLogger struct {
	logger     *logrus.Logger
	config     *LogConfig
	rateLimiter *TokenBucket
	diskMonitor *DiskMonitor
	mu         sync.RWMutex
	enabled    bool
}

// TokenBucket implements a simple token bucket for rate limiting
type TokenBucket struct {
	capacity   int
	tokens     int
	refillRate int
	lastRefill time.Time
	mu         sync.Mutex
}

// DiskMonitor monitors disk usage and cleans up old logs
type DiskMonitor struct {
	config      *LogConfig
	logDir      string
	ticker      *time.Ticker
	stopChan    chan struct{}
	mu          sync.RWMutex
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(capacity, refillRate int) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Allow checks if a token is available
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)
	
	// Refill tokens based on elapsed time
	tokensToRefill := int(elapsed.Seconds()) * tb.refillRate
	if tokensToRefill > 0 {
		tb.tokens += tokensToRefill
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
		tb.lastRefill = now
	}

	if tb.tokens > 0 {
		tb.tokens--
		return true
	}
	return false
}

// NewDiskMonitor creates a new disk monitor
func NewDiskMonitor(config *LogConfig, logDir string) *DiskMonitor {
	dm := &DiskMonitor{
		config:   config,
		logDir:   logDir,
		stopChan: make(chan struct{}),
	}
	
	if config.CleanupInterval > 0 {
		dm.ticker = time.NewTicker(config.CleanupInterval)
		go dm.monitorDiskUsage()
	}
	
	return dm
}

// monitorDiskUsage runs periodic disk usage checks
func (dm *DiskMonitor) monitorDiskUsage() {
	for {
		select {
		case <-dm.ticker.C:
			dm.cleanupIfNeeded()
		case <-dm.stopChan:
			return
		}
	}
}

// cleanupIfNeeded checks disk usage and cleans up if necessary
func (dm *DiskMonitor) cleanupIfNeeded() {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	totalSize, err := dm.calculateLogDirSize()
	if err != nil {
		logrus.Warnf("Failed to calculate log directory size: %v", err)
		return
	}

	maxSizeMB := int64(dm.config.MaxDiskUsageMB) * 1024 * 1024
	if totalSize > maxSizeMB {
		logrus.Warnf("Log directory size (%.2f MB) exceeds limit (%.2f MB), cleaning up...", 
			float64(totalSize)/1024/1024, float64(maxSizeMB)/1024/1024)
		
		if err := dm.cleanupOldLogs(); err != nil {
			logrus.Errorf("Failed to cleanup old logs: %v", err)
		}
	}
}

// calculateLogDirSize calculates total size of log directory
func (dm *DiskMonitor) calculateLogDirSize() (int64, error) {
	var totalSize int64
	
	err := filepath.Walk(dm.logDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".log" {
			totalSize += info.Size()
		}
		return nil
	})
	
	return totalSize, err
}

// cleanupOldLogs removes old log files to free space
func (dm *DiskMonitor) cleanupOldLogs() error {
	files, err := filepath.Glob(filepath.Join(dm.logDir, "*.log*"))
	if err != nil {
		return err
	}

	// Sort files by modification time (oldest first)
	type fileInfo struct {
		path string
		time time.Time
		size int64
	}
	
	var fileInfos []fileInfo
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fileInfo{
			path: file,
			time: info.ModTime(),
			size: info.Size(),
		})
	}

	// Sort by modification time
	for i := 0; i < len(fileInfos)-1; i++ {
		for j := i + 1; j < len(fileInfos); j++ {
			if fileInfos[i].time.After(fileInfos[j].time) {
				fileInfos[i], fileInfos[j] = fileInfos[j], fileInfos[i]
			}
		}
	}

	// Remove oldest files until under limit
	var removedSize int64
	maxSizeMB := int64(dm.config.MaxDiskUsageMB) * 1024 * 1024
	
	for _, info := range fileInfos {
		totalSize, _ := dm.calculateLogDirSize()
		if totalSize-removedSize <= maxSizeMB {
			break
		}
		
		if err := os.Remove(info.path); err != nil {
			logrus.Warnf("Failed to remove old log file %s: %v", info.path, err)
		} else {
			logrus.Infof("Removed old log file: %s (%.2f MB)", info.path, float64(info.size)/1024/1024)
			removedSize += info.size
		}
	}
	
	return nil
}

// Stop stops the disk monitor
func (dm *DiskMonitor) Stop() {
	if dm.ticker != nil {
		dm.ticker.Stop()
	}
	close(dm.stopChan)
}

// SetupLogging configures logging with disk management
func SetupLogging(config *LogConfig) (*RateLimitedLogger, error) {
	logger := logrus.New()

	// Set log level
	level, err := logrus.ParseLevel(config.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	logger.SetLevel(level)

	// Set log format
	switch config.Format {
	case "json":
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
			DisableHTMLEscape: true,
		})
	case "text":
		logger.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: time.RFC3339,
			FullTimestamp:   true,
		})
	default:
		return nil, fmt.Errorf("invalid log format: %s", config.Format)
	}

	// Set output with rotation if it's a file
	var output io.Writer
	var logDir string
	
	switch config.Output {
	case "stdout":
		output = os.Stdout
	case "stderr":
		output = os.Stderr
	default:
		// File output with rotation
		logDir = filepath.Dir(config.Output)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
		
		output = &lumberjack.Logger{
			Filename:   config.Output,
			MaxSize:    config.MaxSize,
			MaxBackups: config.MaxBackups,
			MaxAge:     config.MaxAge,
			Compress:   config.Compress,
		}
	}
	
	logger.SetOutput(output)

	// Create rate limiter if enabled
	var rateLimiter *TokenBucket
	if config.EnableRateLimit {
		rateLimiter = NewTokenBucket(config.BurstLimit, config.RateLimit)
	}

	// Create disk monitor if logging to file
	var diskMonitor *DiskMonitor
	if logDir != "" {
		diskMonitor = NewDiskMonitor(config, logDir)
	}

	return &RateLimitedLogger{
		logger:      logger,
		config:      config,
		rateLimiter: rateLimiter,
		diskMonitor: diskMonitor,
		enabled:     true,
	}, nil
}

// Log methods with rate limiting
func (rl *RateLimitedLogger) shouldLog() bool {
	if !rl.enabled {
		return false
	}
	
	if rl.rateLimiter != nil {
		return rl.rateLimiter.Allow()
	}
	
	return true
}

func (rl *RateLimitedLogger) Error(args ...interface{}) {
	// Always allow error logs
	rl.logger.Error(args...)
}

func (rl *RateLimitedLogger) Errorf(format string, args ...interface{}) {
	// Always allow error logs
	rl.logger.Errorf(format, args...)
}

func (rl *RateLimitedLogger) Warn(args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Warn(args...)
	}
}

func (rl *RateLimitedLogger) Warnf(format string, args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Warnf(format, args...)
	}
}

func (rl *RateLimitedLogger) Info(args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Info(args...)
	}
}

func (rl *RateLimitedLogger) Infof(format string, args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Infof(format, args...)
	}
}

func (rl *RateLimitedLogger) Debug(args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Debug(args...)
	}
}

func (rl *RateLimitedLogger) Debugf(format string, args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Debugf(format, args...)
	}
}

func (rl *RateLimitedLogger) Trace(args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Trace(args...)
	}
}

func (rl *RateLimitedLogger) Tracef(format string, args ...interface{}) {
	if rl.shouldLog() {
		rl.logger.Tracef(format, args...)
	}
}

// Enable/Disable logging
func (rl *RateLimitedLogger) SetEnabled(enabled bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.enabled = enabled
}

// GetStats returns logging statistics
func (rl *RateLimitedLogger) GetStats() map[string]interface{} {
	stats := map[string]interface{}{
		"level":          rl.config.Level,
		"format":         rl.config.Format,
		"output":         rl.config.Output,
		"rate_limited":   rl.config.EnableRateLimit,
		"enabled":        rl.enabled,
	}
	
	if rl.rateLimiter != nil {
		rl.rateLimiter.mu.Lock()
		stats["available_tokens"] = rl.rateLimiter.tokens
		stats["rate_limit"] = rl.rateLimiter.refillRate
		rl.rateLimiter.mu.Unlock()
	}
	
	if rl.diskMonitor != nil {
		totalSize, _ := rl.diskMonitor.calculateLogDirSize()
		stats["disk_usage_mb"] = float64(totalSize) / 1024 / 1024
		stats["disk_limit_mb"] = rl.config.MaxDiskUsageMB
	}
	
	return stats
}

// Shutdown gracefully shuts down the logger
func (rl *RateLimitedLogger) Shutdown() {
	if rl.diskMonitor != nil {
		rl.diskMonitor.Stop()
	}
}
