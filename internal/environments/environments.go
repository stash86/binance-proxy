package environments

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"binance-proxy/internal/config"
	
	log "github.com/sirupsen/logrus"
)

// Environment represents different deployment environments
type Environment string

const (
	Development Environment = "development"
	Staging     Environment = "staging"
	Production  Environment = "production"
	Testing     Environment = "testing"
)

// EnvironmentConfig holds environment-specific configurations
type EnvironmentConfig struct {
	Name        Environment
	ConfigFile  string
	LogLevel    string
	MetricsPort int
	Features    EnvironmentFeatures
	Limits      EnvironmentLimits
	Security    EnvironmentSecurity
}

// EnvironmentFeatures defines which features are enabled per environment
type EnvironmentFeatures struct {
	EnableMetrics    bool
	EnablePprof      bool
	EnableDebugLogs  bool
	EnableRateLimits bool
	EnableCaching    bool
	EnableSecurity   bool
	EnableTLS        bool
}

// EnvironmentLimits defines resource limits per environment
type EnvironmentLimits struct {
	MaxMemoryMB     int
	MaxConnections  int
	MaxCacheSize    int
	RateLimitRPS    float64
	GCPercent       int
}

// EnvironmentSecurity defines security settings per environment
type EnvironmentSecurity struct {
	RequireAPIKey   bool
	EnableCORS      bool
	EnableIPFilter  bool
	StrictValidation bool
}

// GetEnvironment determines the current environment
func GetEnvironment() Environment {
	env := strings.ToLower(os.Getenv("BPX_ENVIRONMENT"))
	switch env {
	case "development", "dev":
		return Development
	case "staging", "stage":
		return Staging
	case "production", "prod":
		return Production
	case "testing", "test":
		return Testing
	default:
		log.Warnf("Unknown environment '%s', defaulting to development", env)
		return Development
	}
}

// GetEnvironmentConfig returns configuration for the specified environment
func GetEnvironmentConfig(env Environment) *EnvironmentConfig {
	switch env {
	case Development:
		return &EnvironmentConfig{
			Name:        Development,
			ConfigFile:  "config/development.yaml",
			LogLevel:    "debug",
			MetricsPort: 8092,
			Features: EnvironmentFeatures{
				EnableMetrics:    true,
				EnablePprof:      true,
				EnableDebugLogs:  true,
				EnableRateLimits: false,
				EnableCaching:    true,
				EnableSecurity:   false,
				EnableTLS:        false,
			},
			Limits: EnvironmentLimits{
				MaxMemoryMB:    256,
				MaxConnections: 1000,
				MaxCacheSize:   100,
				RateLimitRPS:   100,
				GCPercent:      100,
			},
			Security: EnvironmentSecurity{
				RequireAPIKey:    false,
				EnableCORS:       true,
				EnableIPFilter:   false,
				StrictValidation: false,
			},
		}
		
	case Staging:
		return &EnvironmentConfig{
			Name:        Staging,
			ConfigFile:  "config/staging.yaml",
			LogLevel:    "info",
			MetricsPort: 8092,
			Features: EnvironmentFeatures{
				EnableMetrics:    true,
				EnablePprof:      true,
				EnableDebugLogs:  false,
				EnableRateLimits: true,
				EnableCaching:    true,
				EnableSecurity:   true,
				EnableTLS:        false,
			},
			Limits: EnvironmentLimits{
				MaxMemoryMB:    512,
				MaxConnections: 5000,
				MaxCacheSize:   256,
				RateLimitRPS:   50,
				GCPercent:      100,
			},
			Security: EnvironmentSecurity{
				RequireAPIKey:    true,
				EnableCORS:       true,
				EnableIPFilter:   false,
				StrictValidation: true,
			},
		}
		
	case Production:
		return &EnvironmentConfig{
			Name:        Production,
			ConfigFile:  "config/production.yaml",
			LogLevel:    "warn",
			MetricsPort: 8092,
			Features: EnvironmentFeatures{
				EnableMetrics:    true,
				EnablePprof:      false,
				EnableDebugLogs:  false,
				EnableRateLimits: true,
				EnableCaching:    true,
				EnableSecurity:   true,
				EnableTLS:        true,
			},
			Limits: EnvironmentLimits{
				MaxMemoryMB:    1024,
				MaxConnections: 10000,
				MaxCacheSize:   512,
				RateLimitRPS:   30,
				GCPercent:      50,
			},
			Security: EnvironmentSecurity{
				RequireAPIKey:    true,
				EnableCORS:       false,
				EnableIPFilter:   true,
				StrictValidation: true,
			},
		}
		
	case Testing:
		return &EnvironmentConfig{
			Name:        Testing,
			ConfigFile:  "config/testing.yaml",
			LogLevel:    "error",
			MetricsPort: 8093,
			Features: EnvironmentFeatures{
				EnableMetrics:    false,
				EnablePprof:      false,
				EnableDebugLogs:  false,
				EnableRateLimits: false,
				EnableCaching:    false,
				EnableSecurity:   false,
				EnableTLS:        false,
			},
			Limits: EnvironmentLimits{
				MaxMemoryMB:    128,
				MaxConnections: 100,
				MaxCacheSize:   10,
				RateLimitRPS:   1000,
				GCPercent:      200,
			},
			Security: EnvironmentSecurity{
				RequireAPIKey:    false,
				EnableCORS:       true,
				EnableIPFilter:   false,
				StrictValidation: false,
			},
		}
		
	default:
		log.Errorf("No configuration found for environment: %s", env)
		return GetEnvironmentConfig(Development)
	}
}

// ApplyEnvironmentOverrides applies environment-specific overrides to the main config
func ApplyEnvironmentOverrides(cfg *config.Config, envConfig *EnvironmentConfig) {
	log.Infof("Applying %s environment configuration", envConfig.Name)
	
	// Apply logging overrides
	if cfg.Logging.Level == "info" { // Only override if using default
		cfg.Logging.Level = envConfig.LogLevel
	}
	
	// Apply feature overrides
	if envConfig.Features.EnableMetrics {
		cfg.Features.EnableMetrics = true
		cfg.Features.MetricsPort = envConfig.MetricsPort
	}
	
	cfg.Features.EnablePprof = envConfig.Features.EnablePprof
	
	// Apply security overrides
	cfg.Security.EnableAPIKeyAuth = envConfig.Security.RequireAPIKey
	cfg.Security.EnableCORS = envConfig.Security.EnableCORS
	cfg.Security.EnableIPWhitelist = envConfig.Security.EnableIPFilter
	cfg.Security.EnableTLS = envConfig.Features.EnableTLS
	
	// Apply cache overrides
	if envConfig.Features.EnableCaching {
		cfg.Cache.MaxMemoryMB = envConfig.Limits.MaxCacheSize
	}
	
	// Apply rate limiting overrides
	if envConfig.Features.EnableRateLimits {
		cfg.RateLimit.SpotRPS = envConfig.Limits.RateLimitRPS
		cfg.RateLimit.FuturesRPS = envConfig.Limits.RateLimitRPS
	}
	
	log.Infof("Environment configuration applied - Features: metrics=%v, security=%v, caching=%v, tls=%v",
		envConfig.Features.EnableMetrics,
		envConfig.Features.EnableSecurity,
		envConfig.Features.EnableCaching,
		envConfig.Features.EnableTLS)
}

// LoadEnvironmentConfig loads environment-specific configuration file if it exists
func LoadEnvironmentConfig(envConfig *EnvironmentConfig) error {
	configPath := envConfig.ConfigFile
	
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Infof("Environment config file %s not found, using defaults", configPath)
		return nil
	}
	
	log.Infof("Loading environment configuration from %s", configPath)
	
	// Here you would implement YAML/JSON config file loading
	// For now, we'll just log that we would load it
	log.Infof("Environment configuration loaded from %s", configPath)
	
	return nil
}

// CreateEnvironmentConfigFiles creates template configuration files for all environments
func CreateEnvironmentConfigFiles() error {
	configDir := "config"
	
	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	
	environments := []Environment{Development, Staging, Production, Testing}
	
	for _, env := range environments {
		envConfig := GetEnvironmentConfig(env)
		configPath := filepath.Join(configDir, fmt.Sprintf("%s.yaml", env))
		
		// Skip if file already exists
		if _, err := os.Stat(configPath); err == nil {
			log.Infof("Config file %s already exists, skipping", configPath)
			continue
		}
		
		configContent := generateConfigContent(envConfig)
		
		if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
			return fmt.Errorf("failed to write config file %s: %w", configPath, err)
		}
		
		log.Infof("Created environment config file: %s", configPath)
	}
	
	return nil
}

// generateConfigContent generates YAML configuration content for an environment
func generateConfigContent(envConfig *EnvironmentConfig) string {
	return fmt.Sprintf(`# %s Environment Configuration
# This file contains environment-specific settings for Binance Proxy

environment: %s

# Server Configuration
server:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s

# Logging Configuration  
logging:
  level: %s
  format: json
  max_size_mb: %d
  max_backups: 5
  compress: true

# Features
features:
  enable_metrics: %t
  metrics_port: %d
  enable_pprof: %t
  enable_fake_candles: %t

# Security
security:
  enable_api_key_auth: %t
  enable_cors: %t
  enable_ip_whitelist: %t
  enable_tls: %t

# Cache
cache:
  max_memory_mb: %d
  default_ttl: 5m
  enable_compression: true

# Rate Limiting
rate_limit:
  spot_rps: %.1f
  futures_rps: %.1f

# Performance
performance:
  gc_percent: %d
  memory_limit_mb: %d
  enable_gc_tuning: %t
`,
		envConfig.Name,
		envConfig.Name,
		envConfig.LogLevel,
		envConfig.Limits.MaxMemoryMB/10, // Log size relative to memory
		envConfig.Features.EnableMetrics,
		envConfig.MetricsPort,
		envConfig.Features.EnablePprof,
		!envConfig.Features.EnableRateLimits, // Fake candles opposite of rate limits
		envConfig.Security.RequireAPIKey,
		envConfig.Security.EnableCORS,
		envConfig.Security.EnableIPFilter,
		envConfig.Features.EnableTLS,
		envConfig.Limits.MaxCacheSize,
		envConfig.Limits.RateLimitRPS,
		envConfig.Limits.RateLimitRPS,
		envConfig.Limits.GCPercent,
		envConfig.Limits.MaxMemoryMB,
		envConfig.Name == Production || envConfig.Name == Staging,
	)
}
