package tool

import (
	"context"
	"testing"
	"time"
)

func TestDelayIteratorSequenceAndReset(t *testing.T) {
	iter := NewDelayIterator()
	iter.SetDelayList([]time.Duration{time.Millisecond, 2 * time.Millisecond})

	if got := iter.nextDelay(); got != time.Millisecond {
		t.Fatalf("first delay = %v, want 1ms", got)
	}
	if got := iter.nextDelay(); got != 2*time.Millisecond {
		t.Fatalf("second delay = %v, want 2ms", got)
	}
	if got := iter.nextDelay(); got != 2*time.Millisecond {
		t.Fatalf("capped delay = %v, want 2ms", got)
	}

	iter.Reset()
	if got := iter.nextDelay(); got != time.Millisecond {
		t.Fatalf("delay after reset = %v, want 1ms", got)
	}
}

func TestDelayIteratorCopiesDelayList(t *testing.T) {
	delays := []time.Duration{time.Millisecond}
	iter := NewDelayIterator()
	iter.SetDelayList(delays)

	delays[0] = time.Hour

	if got := iter.nextDelay(); got != time.Millisecond {
		t.Fatalf("delay = %v, want copied 1ms", got)
	}
}

func TestDelayIteratorEmptyListUsesDefault(t *testing.T) {
	iter := NewDelayIterator()
	iter.SetDelayList(nil)

	if got := iter.nextDelay(); got != defaultDelayList[0] {
		t.Fatalf("delay = %v, want default first delay %v", got, defaultDelayList[0])
	}
}

func TestDelayIteratorZeroValueUsesDefault(t *testing.T) {
	var iter DelayIterator

	if got := iter.nextDelay(); got != defaultDelayList[0] {
		t.Fatalf("delay = %v, want default first delay %v", got, defaultDelayList[0])
	}
}

func TestDelayContextReturnsFalseWhenCanceled(t *testing.T) {
	iter := NewDelayIterator()
	iter.SetDelayList([]time.Duration{time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if iter.DelayContext(ctx) {
		t.Fatal("DelayContext returned true, want false")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("DelayContext blocked for %v after cancellation", elapsed)
	}
}

func TestDelayContextHandlesZeroDelay(t *testing.T) {
	iter := NewDelayIterator()
	iter.SetDelayList([]time.Duration{0})

	if !iter.DelayContext(context.Background()) {
		t.Fatal("DelayContext returned false for active context and zero delay")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if iter.DelayContext(ctx) {
		t.Fatal("DelayContext returned true for canceled context and zero delay")
	}
}

func TestDelayContextAllowsNilContext(t *testing.T) {
	iter := NewDelayIterator()
	iter.SetDelayList([]time.Duration{0})

	if !iter.DelayContext(nil) {
		t.Fatal("DelayContext returned false for nil context")
	}
}
