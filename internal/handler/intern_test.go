package handler

import (
	"fmt"
	"sync"
	"testing"
)

func TestStringInternerInternsValues(t *testing.T) {
	interner := newStringInterner(1, 2)

	if got := interner.intern("BTCUSDT"); got != "BTCUSDT" {
		t.Fatalf("intern() = %q, want BTCUSDT", got)
	}
	if got := interner.intern("BTCUSDT"); got != "BTCUSDT" {
		t.Fatalf("intern() reused value = %q, want BTCUSDT", got)
	}
	if got := internerCacheLen(interner); got != 1 {
		t.Fatalf("cache len = %d, want 1", got)
	}
}

func TestStringInternerSkipsEmptyString(t *testing.T) {
	interner := newStringInterner(1, 2)

	if got := interner.intern(""); got != "" {
		t.Fatalf("intern(empty) = %q, want empty", got)
	}
	if got := internerCacheLen(interner); got != 0 {
		t.Fatalf("cache len = %d, want 0", got)
	}
}

func TestStringInternerHandlesNilReceiver(t *testing.T) {
	var interner *stringInterner

	if got := interner.intern("BTCUSDT"); got != "BTCUSDT" {
		t.Fatalf("nil intern() = %q, want BTCUSDT", got)
	}
}

func TestStringInternerRespectsMaxEntries(t *testing.T) {
	interner := newStringInterner(4, 2)

	interner.intern("BTCUSDT")
	interner.intern("ETHUSDT")
	interner.intern("BNBUSDT")

	if got := internerCacheLen(interner); got != 2 {
		t.Fatalf("cache len = %d, want 2", got)
	}
	interner.mu.RLock()
	_, cached := interner.cache["BNBUSDT"]
	interner.mu.RUnlock()
	if cached {
		t.Fatal("value above max entries was cached")
	}
}

func TestStringInternerNegativeConfigDisablesCaching(t *testing.T) {
	interner := newStringInterner(-1, -1)

	if got := interner.intern("BTCUSDT"); got != "BTCUSDT" {
		t.Fatalf("intern() = %q, want BTCUSDT", got)
	}
	if got := internerCacheLen(interner); got != 0 {
		t.Fatalf("cache len = %d, want 0", got)
	}
}

func TestStringInternerZeroValueWithMaxEntries(t *testing.T) {
	interner := &stringInterner{maxEntries: 1}

	if got := interner.intern("BTCUSDT"); got != "BTCUSDT" {
		t.Fatalf("intern() = %q, want BTCUSDT", got)
	}
	if got := internerCacheLen(interner); got != 1 {
		t.Fatalf("cache len = %d, want 1", got)
	}
}

func TestStringInternerConcurrentUse(t *testing.T) {
	interner := newStringInterner(2, 4)
	values := []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT", "XRPUSDT", "ADAUSDT"}
	errC := make(chan error, 1)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for _, value := range values {
				if got := interner.intern(value); got != value {
					select {
					case errC <- fmt.Errorf("intern() = %q, want %q", got, value):
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()

	select {
	case err := <-errC:
		t.Fatal(err)
	default:
	}

	if got := internerCacheLen(interner); got > 4 {
		t.Fatalf("cache len = %d, want <= 4", got)
	}
}

func internerCacheLen(interner *stringInterner) int {
	interner.mu.RLock()
	defer interner.mu.RUnlock()

	return len(interner.cache)
}
