package handler

import (
	"sync"
)

const (
	symbolInternerInitialSize   = 35
	symbolInternerMaxEntries    = 256
	intervalInternerInitialSize = 15
	intervalInternerMaxEntries  = 32
)

var (
	symbolIntern   = newStringInterner(symbolInternerInitialSize, symbolInternerMaxEntries)
	intervalIntern = newStringInterner(intervalInternerInitialSize, intervalInternerMaxEntries)
)

type stringInterner struct {
	mu         sync.RWMutex
	cache      map[string]string
	maxEntries int
}

func newStringInterner(initialSize, maxEntries int) *stringInterner {
	initialSize = nonNegative(initialSize)
	maxEntries = nonNegative(maxEntries)
	if maxEntries > 0 && initialSize > maxEntries {
		initialSize = maxEntries
	}

	return &stringInterner{
		cache:      make(map[string]string, initialSize),
		maxEntries: maxEntries,
	}
}

func (si *stringInterner) intern(s string) string {
	if si == nil || s == "" {
		return s
	}

	si.mu.RLock()
	if interned, exists := si.cache[s]; exists {
		si.mu.RUnlock()
		return interned
	}
	si.mu.RUnlock()

	si.mu.Lock()
	defer si.mu.Unlock()

	if interned, exists := si.cache[s]; exists {
		return interned
	}
	if si.maxEntries == 0 || len(si.cache) >= si.maxEntries {
		return s
	}
	if si.cache == nil {
		si.cache = make(map[string]string, si.maxEntries)
	}

	si.cache[s] = s
	return s
}

func InternSymbol(symbol string) string {
	return symbolIntern.intern(symbol)
}

func InternInterval(interval string) string {
	return intervalIntern.intern(interval)
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}

	return n
}
