package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// SecurityManager handles authentication and rate limiting
type SecurityManager struct {
	apiKeys       map[string]*APIKey
	rateLimiters  map[string]*ClientRateLimiter
	config        *SecurityConfig
	mu            sync.RWMutex
}

// APIKey represents an API key with metadata
type APIKey struct {
	Key         string
	Name        string
	Permissions []string
	CreatedAt   time.Time
	LastUsed    time.Time
	UsageCount  int64
	RateLimit   int
	Enabled     bool
}

// ClientRateLimiter tracks rate limiting per client
type ClientRateLimiter struct {
	tokens     int
	lastRefill time.Time
	limit      int
	window     time.Duration
}

// SecurityConfig holds security configuration
type SecurityConfig struct {
	EnableAuth        bool          `long:"enable-auth" env:"ENABLE_AUTH" description:"Enable API key authentication"`
	EnableRateLimit   bool          `long:"enable-rate-limit" env:"ENABLE_RATE_LIMIT" description:"Enable per-client rate limiting"`
	DefaultRateLimit  int           `long:"default-rate-limit" env:"DEFAULT_RATE_LIMIT" description:"Default rate limit per minute" default:"1000"`
	RateLimitWindow   time.Duration `long:"rate-limit-window" env:"RATE_LIMIT_WINDOW" description:"Rate limit window" default:"1m"`
	EnableCORS        bool          `long:"enable-cors" env:"ENABLE_CORS" description:"Enable CORS headers"`
	TrustedProxies    []string      `long:"trusted-proxies" env:"TRUSTED_PROXIES" description:"Trusted proxy IPs" env-delim:","`
	MaxRequestSize    int64         `long:"max-request-size" env:"MAX_REQUEST_SIZE" description:"Maximum request size in bytes" default:"1048576"`
	EnableTLS         bool          `long:"enable-tls" env:"ENABLE_TLS" description:"Enable TLS"`
	TLSCertFile       string        `long:"tls-cert-file" env:"TLS_CERT_FILE" description:"TLS certificate file path"`
	TLSKeyFile        string        `long:"tls-key-file" env:"TLS_KEY_FILE" description:"TLS private key file path"`
}

// NewSecurityManager creates a new security manager
func NewSecurityManager(config *SecurityConfig) *SecurityManager {
	return &SecurityManager{
		apiKeys:      make(map[string]*APIKey),
		rateLimiters: make(map[string]*ClientRateLimiter),
		config:       config,
	}
}

// GenerateAPIKey generates a new API key
func (sm *SecurityManager) GenerateAPIKey(name string, permissions []string, rateLimit int) (*APIKey, error) {
	// Generate random key
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	
	key := hex.EncodeToString(bytes)
	
	apiKey := &APIKey{
		Key:         key,
		Name:        name,
		Permissions: permissions,
		CreatedAt:   time.Now(),
		RateLimit:   rateLimit,
		Enabled:     true,
	}
	
	sm.mu.Lock()
	sm.apiKeys[key] = apiKey
	sm.mu.Unlock()
	
	logrus.Infof("Generated new API key for %s with permissions: %v", name, permissions)
	return apiKey, nil
}

// ValidateAPIKey validates an API key and returns the associated metadata
func (sm *SecurityManager) ValidateAPIKey(key string) (*APIKey, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	apiKey, exists := sm.apiKeys[key]
	if !exists || !apiKey.Enabled {
		return nil, false
	}
	
	// Update usage stats
	apiKey.LastUsed = time.Now()
	apiKey.UsageCount++
	
	return apiKey, true
}

// CheckRateLimit checks if a client has exceeded rate limits
func (sm *SecurityManager) CheckRateLimit(clientID string, customLimit ...int) bool {
	if !sm.config.EnableRateLimit {
		return true
	}
	
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	limiter, exists := sm.rateLimiters[clientID]
	if !exists {
		limit := sm.config.DefaultRateLimit
		if len(customLimit) > 0 {
			limit = customLimit[0]
		}
		
		limiter = &ClientRateLimiter{
			tokens:     limit,
			lastRefill: time.Now(),
			limit:      limit,
			window:     sm.config.RateLimitWindow,
		}
		sm.rateLimiters[clientID] = limiter
	}
	
	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(limiter.lastRefill)
	if elapsed >= limiter.window {
		limiter.tokens = limiter.limit
		limiter.lastRefill = now
	}
	
	if limiter.tokens > 0 {
		limiter.tokens--
		return true
	}
	
	return false
}

// SecurityMiddleware returns an HTTP middleware for security
func (sm *SecurityManager) SecurityMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Set security headers
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			
			// CORS headers if enabled
			if sm.config.EnableCORS {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
				
				if r.Method == "OPTIONS" {
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			
			// Check request size
			if r.ContentLength > sm.config.MaxRequestSize {
				http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
				return
			}
			
			// Get client ID (IP or API key)
			clientID := sm.getClientID(r)
			
			// API key authentication if enabled
			if sm.config.EnableAuth {
				apiKey := sm.extractAPIKey(r)
				if apiKey == "" {
					http.Error(w, "API key required", http.StatusUnauthorized)
					return
				}
				
				keyData, valid := sm.ValidateAPIKey(apiKey)
				if !valid {
					http.Error(w, "Invalid API key", http.StatusUnauthorized)
					return
				}
				
				// Use API key for rate limiting
				clientID = apiKey
				
				// Check permissions (basic implementation)
				if !sm.checkPermissions(keyData, r) {
					http.Error(w, "Insufficient permissions", http.StatusForbidden)
					return
				}
			}
			
			// Rate limiting
			if !sm.CheckRateLimit(clientID) {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			
			next.ServeHTTP(w, r)
		})
	}
}

// getClientID extracts client identifier from request
func (sm *SecurityManager) getClientID(r *http.Request) string {
	// Try to get real IP from trusted proxies
	if len(sm.config.TrustedProxies) > 0 {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ips := strings.Split(forwarded, ",")
			if len(ips) > 0 {
				return strings.TrimSpace(ips[0])
			}
		}
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return realIP
		}
	}
	
	// Fall back to remote address
	return r.RemoteAddr
}

// extractAPIKey extracts API key from request
func (sm *SecurityManager) extractAPIKey(r *http.Request) string {
	// Try header first
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	
	// Try Authorization header
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	
	// Try query parameter
	return r.URL.Query().Get("api_key")
}

// checkPermissions checks if API key has required permissions
func (sm *SecurityManager) checkPermissions(apiKey *APIKey, r *http.Request) bool {
	// Basic permission check - can be extended
	for _, permission := range apiKey.Permissions {
		switch permission {
		case "read":
			if r.Method == "GET" {
				return true
			}
		case "write":
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
				return true
			}
		case "admin":
			return true
		case "*":
			return true
		}
	}
	
	return len(apiKey.Permissions) == 0 // Allow if no specific permissions set
}

// Stats represents security statistics
type Stats struct {
	APIKeysCount      int     `json:"api_keys_count"`
	RateLimitersCount int     `json:"rate_limiters_count"`
	AuthEnabled       bool    `json:"auth_enabled"`
	RateLimitEnabled  bool    `json:"rate_limit_enabled"`
	CORSEnabled       bool    `json:"cors_enabled"`
	TLSEnabled        bool    `json:"tls_enabled"`
	TotalAPIUsage     int64   `json:"total_api_usage"`
	EnabledKeys       int     `json:"enabled_keys"`
	BlockedRequests   int64   `json:"blocked_requests"`
}

// GetStats returns security statistics
func (sm *SecurityManager) GetStats() *Stats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	stats := &Stats{
		APIKeysCount:      len(sm.apiKeys),
		RateLimitersCount: len(sm.rateLimiters),
		AuthEnabled:       sm.config.EnableAuth,
		RateLimitEnabled:  sm.config.EnableRateLimit,
		CORSEnabled:       sm.config.EnableCORS,
		TLSEnabled:        sm.config.EnableTLS,
	}
	
	// API key usage stats
	var totalUsage int64
	enabledKeys := 0
	for _, key := range sm.apiKeys {
		totalUsage += key.UsageCount
		if key.Enabled {
			enabledKeys++
		}
	}
	
	stats.TotalAPIUsage = totalUsage
	stats.EnabledKeys = enabledKeys
	
	return stats
}

// Cleanup removes old rate limiters to prevent memory leaks
func (sm *SecurityManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	cutoff := time.Now().Add(-time.Hour) // Remove limiters older than 1 hour
	
	for clientID, limiter := range sm.rateLimiters {
		if limiter.lastRefill.Before(cutoff) {
			delete(sm.rateLimiters, clientID)
		}
	}
	
	logrus.Debugf("Security cleanup completed, %d rate limiters remaining", len(sm.rateLimiters))
}

// SecureCompare performs constant-time string comparison
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// IsHealthy returns whether the security manager is healthy
func (sm *SecurityManager) IsHealthy() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	// Consider healthy if not overloaded with rate limiters
	return len(sm.rateLimiters) < 10000
}
