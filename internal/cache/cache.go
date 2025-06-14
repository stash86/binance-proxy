package cache

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Cache provides intelligent caching with TTL and memory management
type Cache struct {
	items       map[string]*CacheItem
	mu          sync.RWMutex
	maxSize     int
	maxMemoryMB int
	ttl         time.Duration
	cleanup     *time.Ticker
	stats       *CacheStats
}

// CacheItem represents a cached item
type CacheItem struct {
	Data      interface{}
	ExpiresAt time.Time
	AccessCount int64
	LastAccess  time.Time
	Size        int
	Key         string
}

// CacheStats tracks cache performance
type CacheStats struct {
	Hits           int64
	Misses         int64
	Evictions      int64
	Items          int64
	TotalSize      int64
	HitRatio       float64
	LastCleanup    time.Time
	CleanupCount   int64
}

// CacheConfig holds cache configuration
type CacheConfig struct {
	MaxSize     int           `long:"max-size" env:"MAX_SIZE" description:"Maximum number of cached items" default:"10000"`
	MaxMemoryMB int           `long:"max-memory-mb" env:"MAX_MEMORY_MB" description:"Maximum memory usage in MB" default:"100"`
	TTL         time.Duration `long:"ttl" env:"TTL" description:"Default time-to-live for cache items" default:"5m"`
	CleanupInterval time.Duration `long:"cleanup-interval" env:"CLEANUP_INTERVAL" description:"Cleanup interval" default:"1m"`
	EnableStats bool          `long:"enable-stats" env:"ENABLE_STATS" description:"Enable cache statistics" default:"true"`
}

// NewCache creates a new cache instance
func NewCache(config *CacheConfig) *Cache {
	cache := &Cache{
		items:       make(map[string]*CacheItem),
		maxSize:     config.MaxSize,
		maxMemoryMB: config.MaxMemoryMB,
		ttl:         config.TTL,
		stats:       &CacheStats{},
	}
	
	// Start cleanup routine
	cache.cleanup = time.NewTicker(config.CleanupInterval)
	go cache.cleanupRoutine()
	
	return cache
}

// Set stores an item in the cache
func (c *Cache) Set(key string, data interface{}, ttl ...time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Calculate item size
	size := c.calculateSize(data)
	
	// Check if we need to make space
	if err := c.makeSpace(size); err != nil {
		return fmt.Errorf("failed to make space for cache item: %w", err)
	}
	
	// Determine TTL
	itemTTL := c.ttl
	if len(ttl) > 0 {
		itemTTL = ttl[0]
	}
	
	item := &CacheItem{
		Data:        data,
		ExpiresAt:   time.Now().Add(itemTTL),
		AccessCount: 0,
		LastAccess:  time.Now(),
		Size:        size,
		Key:         key,
	}
	
	// Remove existing item if present
	if existing, exists := c.items[key]; exists {
		c.stats.TotalSize -= int64(existing.Size)
		c.stats.Items--
	}
	
	c.items[key] = item
	c.stats.Items++
	c.stats.TotalSize += int64(size)
	
	logrus.Tracef("Cache: stored item %s (size: %d bytes, ttl: %v)", key, size, itemTTL)
	return nil
}

// Get retrieves an item from the cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	item, exists := c.items[key]
	if !exists {
		c.stats.Misses++
		c.updateHitRatio()
		return nil, false
	}
	
	// Check if item has expired
	if time.Now().After(item.ExpiresAt) {
		delete(c.items, key)
		c.stats.Items--
		c.stats.TotalSize -= int64(item.Size)
		c.stats.Misses++
		c.stats.Evictions++
		c.updateHitRatio()
		logrus.Tracef("Cache: item %s expired", key)
		return nil, false
	}
	
	// Update access stats
	item.AccessCount++
	item.LastAccess = time.Now()
	c.stats.Hits++
	c.updateHitRatio()
	
	logrus.Tracef("Cache: retrieved item %s (access count: %d)", key, item.AccessCount)
	return item.Data, true
}

// Delete removes an item from the cache
func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	item, exists := c.items[key]
	if !exists {
		return false
	}
	
	delete(c.items, key)
	c.stats.Items--
	c.stats.TotalSize -= int64(item.Size)
	
	logrus.Tracef("Cache: deleted item %s", key)
	return true
}

// Clear removes all items from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	count := len(c.items)
	c.items = make(map[string]*CacheItem)
	c.stats.Items = 0
	c.stats.TotalSize = 0
	
	logrus.Infof("Cache: cleared %d items", count)
}

// GetOrSet retrieves an item or sets it if not found
func (c *Cache) GetOrSet(key string, generator func() (interface{}, error), ttl ...time.Duration) (interface{}, error) {
	// Try to get first
	if data, exists := c.Get(key); exists {
		return data, nil
	}
	
	// Generate new data
	data, err := generator()
	if err != nil {
		return nil, err
	}
	
	// Store in cache
	if err := c.Set(key, data, ttl...); err != nil {
		logrus.Warnf("Failed to cache generated data for key %s: %v", key, err)
	}
	
	return data, nil
}

// calculateSize estimates the size of data in bytes
func (c *Cache) calculateSize(data interface{}) int {
	// Simple size estimation
	switch v := data.(type) {
	case string:
		return len(v)
	case []byte:
		return len(v)
	case int, int8, int16, int32, int64:
		return 8
	case float32, float64:
		return 8
	case bool:
		return 1
	default:
		// Use JSON marshaling for complex types
		if b, err := json.Marshal(v); err == nil {
			return len(b)
		}
		return 100 // Default estimate
	}
}

// makeSpace ensures there's enough space for a new item
func (c *Cache) makeSpace(newItemSize int) error {
	maxSizeBytes := int64(c.maxMemoryMB) * 1024 * 1024
	
	// Check if we need to free space
	for (len(c.items) >= c.maxSize) || (c.stats.TotalSize+int64(newItemSize) > maxSizeBytes) {
		if len(c.items) == 0 {
			return fmt.Errorf("cache item too large: %d bytes exceeds limit", newItemSize)
		}
		
		// Evict least recently used item
		oldestKey := ""
		oldestTime := time.Now()
		
		for key, item := range c.items {
			if item.LastAccess.Before(oldestTime) {
				oldestTime = item.LastAccess
				oldestKey = key
			}
		}
		
		if oldestKey != "" {
			item := c.items[oldestKey]
			delete(c.items, oldestKey)
			c.stats.Items--
			c.stats.TotalSize -= int64(item.Size)
			c.stats.Evictions++
			logrus.Tracef("Cache: evicted item %s (LRU)", oldestKey)
		}
	}
	
	return nil
}

// cleanupRoutine runs periodic cleanup
func (c *Cache) cleanupRoutine() {
	for range c.cleanup.C {
		c.performCleanup()
	}
}

// performCleanup removes expired items
func (c *Cache) performCleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	expiredKeys := make([]string, 0)
	
	for key, item := range c.items {
		if now.After(item.ExpiresAt) {
			expiredKeys = append(expiredKeys, key)
		}
	}
	
	for _, key := range expiredKeys {
		item := c.items[key]
		delete(c.items, key)
		c.stats.Items--
		c.stats.TotalSize -= int64(item.Size)
		c.stats.Evictions++
	}
	
	c.stats.LastCleanup = now
	c.stats.CleanupCount++
	
	if len(expiredKeys) > 0 {
		logrus.Debugf("Cache: cleaned up %d expired items", len(expiredKeys))
	}
}

// updateHitRatio calculates the cache hit ratio
func (c *Cache) updateHitRatio() {
	total := c.stats.Hits + c.stats.Misses
	if total > 0 {
		c.stats.HitRatio = float64(c.stats.Hits) / float64(total)
	}
}

// GetStats returns cache statistics
func (c *Cache) GetStats() *CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Return a copy of stats
	stats := *c.stats
	return &stats
}

// Stop gracefully stops the cache
func (c *Cache) Stop() {
	if c.cleanup != nil {
		c.cleanup.Stop()
	}
	logrus.Info("Cache stopped")
}

// GetMemoryUsageMB returns current memory usage in MB
func (c *Cache) GetMemoryUsageMB() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return float64(c.stats.TotalSize) / 1024 / 1024
}

// GetItemCount returns the number of items in cache
func (c *Cache) GetItemCount() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats.Items
}

// Keys returns all cache keys
func (c *Cache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	keys := make([]string, 0, len(c.items))
	for key := range c.items {
		keys = append(keys, key)
	}
	return keys
}

// Contains checks if a key exists in the cache
func (c *Cache) Contains(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	item, exists := c.items[key]
	if !exists {
		return false
	}
	
	// Check expiration
	return time.Now().Before(item.ExpiresAt)
}

// IsHealthy returns whether the cache is healthy
func (c *Cache) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// Consider healthy if cache is operational and not overloaded
	return c.stats.Items < int64(c.maxSize) && 
		   c.stats.TotalSize < int64(c.maxMemoryMB*1024*1024)
}
