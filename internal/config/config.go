package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

// Config holds all configuration parameters
type Config struct {
	// Server configuration
	Server ServerConfig `group:"server" namespace:"server" env-namespace:"BPX_SERVER"`
	
	// Market configuration
	Markets MarketConfig `group:"markets" namespace:"markets" env-namespace:"BPX_MARKETS"`
	
	// WebSocket configuration
	WebSocket WebSocketConfig `group:"websocket" namespace:"websocket" env-namespace:"BPX_WS"`
	
	// Rate limiting configuration
	RateLimit RateLimitConfig `group:"ratelimit" namespace:"ratelimit" env-namespace:"BPX_RATE"`
	
	// Logging configuration
	Logging LoggingConfig `group:"logging" namespace:"logging" env-namespace:"BPX_LOG"`
	
	// Feature flags
	Features FeatureConfig `group:"features" namespace:"features" env-namespace:"BPX_FEAT"`
	
	// Security configuration
	Security SecurityConfig `group:"security" namespace:"security" env-namespace:"BPX_SEC"`
	
	// Cache configuration
	Cache CacheConfig `group:"cache" namespace:"cache" env-namespace:"BPX_CACHE"`
}

type ServerConfig struct {
	SpotPort      int           `short:"p" long:"port-spot" env:"PORT_SPOT" description:"Port for SPOT markets" default:"8090"`
	FuturesPort   int           `short:"t" long:"port-futures" env:"PORT_FUTURES" description:"Port for FUTURES markets" default:"8091"`
	ReadTimeout   time.Duration `long:"read-timeout" env:"READ_TIMEOUT" description:"HTTP read timeout" default:"30s"`
	WriteTimeout  time.Duration `long:"write-timeout" env:"WRITE_TIMEOUT" description:"HTTP write timeout" default:"30s"`
	IdleTimeout   time.Duration `long:"idle-timeout" env:"IDLE_TIMEOUT" description:"HTTP idle timeout" default:"120s"`
	MaxHeaderSize int           `long:"max-header-size" env:"MAX_HEADER_SIZE" description:"Maximum HTTP header size" default:"8192"`
}

type MarketConfig struct {
	DisableSpot    bool `short:"s" long:"disable-spot" env:"DISABLE_SPOT" description:"Disable proxying spot markets"`
	DisableFutures bool `short:"f" long:"disable-futures" env:"DISABLE_FUTURES" description:"Disable proxying futures markets"`
}

type WebSocketConfig struct {
	// Connection settings
	HandshakeTimeout  time.Duration `long:"handshake-timeout" env:"HANDSHAKE_TIMEOUT" description:"WebSocket handshake timeout" default:"30s"`
	ReconnectDelay    time.Duration `long:"reconnect-delay" env:"RECONNECT_DELAY" description:"Base delay between reconnection attempts" default:"1s"`
	MaxReconnectDelay time.Duration `long:"max-reconnect-delay" env:"MAX_RECONNECT_DELAY" description:"Maximum delay between reconnection attempts" default:"60s"`
	PingInterval      time.Duration `long:"ping-interval" env:"PING_INTERVAL" description:"WebSocket ping interval" default:"30s"`
	PongTimeout       time.Duration `long:"pong-timeout" env:"PONG_TIMEOUT" description:"WebSocket pong timeout" default:"60s"`
	BufferSize        int           `long:"buffer-size" env:"BUFFER_SIZE" description:"WebSocket buffer size" default:"4096"`
	
	// Reconnection settings
	MaxReconnects     int           `long:"max-reconnects" env:"MAX_RECONNECTS" description:"Maximum reconnection attempts" default:"10"`
	
	// Performance settings
	EnableCompression bool          `long:"enable-compression" env:"ENABLE_COMPRESSION" description:"Enable WebSocket compression" default:"true"`
	MessageQueueSize  int           `long:"message-queue-size" env:"MESSAGE_QUEUE_SIZE" description:"Message queue buffer size" default:"1000"`
	
	// Monitoring settings
	EnableHealthCheck bool          `long:"enable-health-check" env:"ENABLE_HEALTH_CHECK" description:"Enable WebSocket health monitoring" default:"true"`
	HealthCheckInterval time.Duration `long:"health-check-interval" env:"HEALTH_CHECK_INTERVAL" description:"Health check interval" default:"30s"`
}

type RateLimitConfig struct {
	SpotRPS     float64 `long:"spot-rps" env:"SPOT_RPS" description:"Spot market requests per second" default:"20"`
	SpotBurst   int     `long:"spot-burst" env:"SPOT_BURST" description:"Spot market burst capacity" default:"1200"`
	FuturesRPS  float64 `long:"futures-rps" env:"FUTURES_RPS" description:"Futures market requests per second" default:"40"`
	FuturesBurst int    `long:"futures-burst" env:"FUTURES_BURST" description:"Futures market burst capacity" default:"2400"`
}

type LoggingConfig struct {
	Level           string        `short:"v" long:"verbose" env:"VERBOSE" description:"Log level (trace, debug, info, warn, error)" default:"info"`
	Format          string        `long:"log-format" env:"LOG_FORMAT" description:"Log format (text, json)" default:"text"`
	Output          string        `long:"log-output" env:"LOG_OUTPUT" description:"Log output (stdout, stderr, file path)" default:"stdout"`
	DisableColors   bool          `long:"disable-colors" env:"DISABLE_COLORS" description:"Disable colored output"`
	ShowForwards    bool          `short:"a" long:"always-show-forwards" env:"ALWAYS_SHOW_FORWARDS" description:"Always show requests forwarded via REST"`
	
	// File rotation and disk management
	MaxSize         int           `long:"log-max-size-mb" env:"LOG_MAX_SIZE_MB" description:"Maximum log file size in MB" default:"100"`
	MaxBackups      int           `long:"log-max-backups" env:"LOG_MAX_BACKUPS" description:"Maximum number of backup files" default:"5"`
	MaxAge          int           `long:"log-max-age-days" env:"LOG_MAX_AGE_DAYS" description:"Maximum age of log files in days" default:"30"`
	Compress        bool          `long:"log-compress" env:"LOG_COMPRESS" description:"Compress backup log files" default:"true"`
	
	// Rate limiting for high-volume logs
	EnableRateLimit bool          `long:"log-enable-rate-limit" env:"LOG_ENABLE_RATE_LIMIT" description:"Enable log rate limiting for debug/trace" default:"false"`
	RateLimit       int           `long:"log-rate-limit" env:"LOG_RATE_LIMIT" description:"Log rate limit per second" default:"100"`
	BurstLimit      int           `long:"log-burst-limit" env:"LOG_BURST_LIMIT" description:"Log burst limit" default:"200"`
	
	// Disk space protection
	MaxDiskUsageMB  int           `long:"log-max-disk-mb" env:"LOG_MAX_DISK_MB" description:"Maximum disk usage for logs in MB" default:"1000"`
	CleanupInterval time.Duration `long:"log-cleanup-interval" env:"LOG_CLEANUP_INTERVAL" description:"Log cleanup check interval" default:"1h"`
}

type FeatureConfig struct {
	DisableFakeKline bool          `short:"c" long:"disable-fake-candles" env:"DISABLE_FAKE_CANDLES" description:"Disable generation of fake candles"`
	EnableMetrics    bool          `long:"enable-metrics" env:"ENABLE_METRICS" description:"Enable metrics endpoint"`
	MetricsPort      int           `long:"metrics-port" env:"METRICS_PORT" description:"Metrics server port" default:"8092"`
	EnablePprof      bool          `long:"enable-pprof" env:"ENABLE_PPROF" description:"Enable pprof endpoints" default:"true"`
	CacheExpiry      time.Duration `long:"cache-expiry" env:"CACHE_EXPIRY" description:"Cache expiry time for inactive connections" default:"2m"`
}

type SecurityConfig struct {
	// API Key Authentication
	EnableAPIKeyAuth bool          `long:"enable-api-key-auth" env:"ENABLE_API_KEY_AUTH" description:"Enable API key authentication"`
	APIKeyHeader     string        `long:"api-key-header" env:"API_KEY_HEADER" description:"API key header name" default:"X-API-Key"`
	APIKeysFile      string        `long:"api-keys-file" env:"API_KEYS_FILE" description:"Path to API keys file"`
	
	// Rate Limiting
	EnableRateLimit  bool          `long:"enable-security-rate-limit" env:"ENABLE_SECURITY_RATE_LIMIT" description:"Enable per-client rate limiting"`
	DefaultRPS       float64       `long:"default-rps" env:"DEFAULT_RPS" description:"Default requests per second per client" default:"10"`
	DefaultBurst     int           `long:"default-burst" env:"DEFAULT_BURST" description:"Default burst capacity per client" default:"20"`
	
	// TLS Configuration
	EnableTLS        bool          `long:"enable-tls" env:"ENABLE_TLS" description:"Enable TLS/HTTPS"`
	TLSCertFile      string        `long:"tls-cert-file" env:"TLS_CERT_FILE" description:"Path to TLS certificate file"`
	TLSKeyFile       string        `long:"tls-key-file" env:"TLS_KEY_FILE" description:"Path to TLS private key file"`
	
	// CORS Configuration
	EnableCORS       bool          `long:"enable-cors" env:"ENABLE_CORS" description:"Enable CORS support"`
	CORSOrigins      []string      `long:"cors-origins" env:"CORS_ORIGINS" description:"Allowed CORS origins"`
	CORSMethods      []string      `long:"cors-methods" env:"CORS_METHODS" description:"Allowed CORS methods"`
	CORSHeaders      []string      `long:"cors-headers" env:"CORS_HEADERS" description:"Allowed CORS headers"`
	
	// Request Validation
	MaxRequestSize   int64         `long:"max-request-size" env:"MAX_REQUEST_SIZE" description:"Maximum request body size in bytes" default:"1048576"`
	EnableIPWhitelist bool         `long:"enable-ip-whitelist" env:"ENABLE_IP_WHITELIST" description:"Enable IP whitelist"`
	WhitelistIPs     []string      `long:"whitelist-ips" env:"WHITELIST_IPS" description:"Whitelisted IP addresses"`
}

type CacheConfig struct {
	// Memory settings
	MaxMemoryMB      int           `long:"cache-max-memory-mb" env:"CACHE_MAX_MEMORY_MB" description:"Maximum cache memory in MB" default:"256"`
	MaxEntries       int           `long:"cache-max-entries" env:"CACHE_MAX_ENTRIES" description:"Maximum number of cache entries" default:"10000"`
	
	// TTL settings
	DefaultTTL       time.Duration `long:"cache-default-ttl" env:"CACHE_DEFAULT_TTL" description:"Default cache TTL" default:"5m"`
	MaxTTL           time.Duration `long:"cache-max-ttl" env:"CACHE_MAX_TTL" description:"Maximum cache TTL" default:"1h"`
	
	// Performance settings
	EnableCompression bool          `long:"cache-enable-compression" env:"CACHE_ENABLE_COMPRESSION" description:"Enable cache compression"`
	CleanupInterval  time.Duration `long:"cache-cleanup-interval" env:"CACHE_CLEANUP_INTERVAL" description:"Cache cleanup interval" default:"1m"`
	
	// Statistics
	EnableStats      bool          `long:"cache-enable-stats" env:"CACHE_ENABLE_STATS" description:"Enable cache statistics" default:"true"`
}

// LoadConfig loads configuration from command line arguments and environment variables
func LoadConfig() (*Config, error) {
	config := &Config{}
	parser := flags.NewParser(config, flags.Default)
	
	// Parse command line arguments
	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			return nil, fmt.Errorf("help requested")
		}
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}
	
	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	
	return config, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Markets.DisableSpot && c.Markets.DisableFutures {
		return fmt.Errorf("cannot disable both spot and futures markets")
	}
	
	if c.Server.SpotPort <= 0 || c.Server.SpotPort > 65535 {
		return fmt.Errorf("invalid spot port: %d", c.Server.SpotPort)
	}
	
	if c.Server.FuturesPort <= 0 || c.Server.FuturesPort > 65535 {
		return fmt.Errorf("invalid futures port: %d", c.Server.FuturesPort)
	}
	
	if c.Features.MetricsPort <= 0 || c.Features.MetricsPort > 65535 {
		return fmt.Errorf("invalid metrics port: %d", c.Features.MetricsPort)
	}
	
	// Validate log level
	switch c.Logging.Level {
	case "trace", "debug", "info", "warn", "error":
		// Valid levels
	default:
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}
	
	// Validate log format
	switch c.Logging.Format {
	case "text", "json":
		// Valid formats
	default:
		return fmt.Errorf("invalid log format: %s", c.Logging.Format)
	}
	
	return nil
}

// SetupLogging configures the logging system based on configuration
func (c *Config) SetupLogging() error {
	// Set log level
	level, err := log.ParseLevel(c.Logging.Level)
	if err != nil {
		return fmt.Errorf("invalid log level %s: %w", c.Logging.Level, err)
	}
	log.SetLevel(level)
	
	// Set log format
	switch c.Logging.Format {
	case "json":
		log.SetFormatter(&log.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	default:
		log.SetFormatter(&log.TextFormatter{
			DisableColors:   c.Logging.DisableColors,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		})
	}
	
	// Set output
	switch c.Logging.Output {
	case "stdout":
		log.SetOutput(os.Stdout)
	case "stderr":
		log.SetOutput(os.Stderr)
	default:
		// Assume it's a file path
		if err := os.MkdirAll(filepath.Dir(c.Logging.Output), 0755); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}
		
		file, err := os.OpenFile(c.Logging.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		log.SetOutput(file)
	}
	
	return nil
}

// GetDisplayName returns a human-readable configuration summary
func (c *Config) GetDisplayName() string {
	return fmt.Sprintf("Spot:%d Futures:%d Metrics:%d", 
		c.Server.SpotPort, c.Server.FuturesPort, c.Features.MetricsPort)
}
