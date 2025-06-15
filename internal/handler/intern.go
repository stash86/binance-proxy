package handler

import (
	"sync"
)

// Optimized for exactly 30 trading pairs
var (
	symbolIntern   = newStringInterner(35) // 30 pairs + 5 buffer for growth
	intervalIntern = newStringInterner(15) // Standard intervals
)

type stringInterner struct {
	mu    sync.RWMutex
	cache map[string]string
}

func newStringInterner(initialSize int) *stringInterner {
	return &stringInterner{
		cache: make(map[string]string, initialSize),
	}
}

func (si *stringInterner) intern(s string) string {
	if s == "" {
		return s
	}

	// Fast path: read-only lookup (most common case)
	si.mu.RLock()
	if interned, exists := si.cache[s]; exists {
		si.mu.RUnlock()
		return interned
	}
	si.mu.RUnlock()

	// Slow path: add new string (rare after startup)
	si.mu.Lock()
	defer si.mu.Unlock()

	// Double-check after acquiring write lock
	if interned, exists := si.cache[s]; exists {
		return interned
	}

	// Store the string
	si.cache[s] = s
	return s
}

// Public API
func InternSymbol(symbol string) string {
	return symbolIntern.intern(symbol)
}

func InternInterval(interval string) string {
	return intervalIntern.intern(interval)
}
