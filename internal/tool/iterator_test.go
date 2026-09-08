package tool

import (
	"context"
	"testing"
	"testing/synctest"
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

func TestDelayContextKeepsExactDelays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		iter := NewDelayIterator()
		iter.SetDelayList([]time.Duration{0, time.Second, 3 * time.Second})
		for _, want := range []time.Duration{0, time.Second, 3 * time.Second, 3 * time.Second} {
			start := time.Now()
			if !iter.DelayContext(context.Background()) {
				t.Fatal("DelayContext returned false for active context")
			}
			if got := time.Since(start); got != want {
				t.Fatalf("delay = %v, want %v", got, want)
			}
		}
	})
}

func TestDelayContextWithJitterBoundsAndCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		delays := []time.Duration{
			time.Nanosecond, 3 * time.Nanosecond,
			2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, 32 * time.Second, time.Minute,
		}
		iter := NewDelayIterator()
		iter.SetDelayList(delays)
		for i := 0; i < len(delays)+64; i++ {
			base := delays[min(i, len(delays)-1)]
			minimum := base/2 + base%2
			start := time.Now()
			if !iter.DelayContextWithJitter(context.Background()) {
				t.Fatal("DelayContextWithJitter returned false for active context")
			}
			if got := time.Since(start); got < minimum || got > base {
				t.Fatalf("delay %d = %v, want between %v and %v", i, got, minimum, base)
			}
		}
		iter.Reset()
		start := time.Now()
		if !iter.DelayContextWithJitter(nil) {
			t.Fatal("DelayContextWithJitter returned false for nil context")
		}
		if got := time.Since(start); got != time.Nanosecond {
			t.Fatalf("delay after reset = %v, want 1ns", got)
		}
	})
}

func TestDelayContextWithJitterCancellation(t *testing.T) {
	for _, delay := range []time.Duration{-time.Second, 0, time.Hour} {
		t.Run(delay.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				iter := NewDelayIterator()
				iter.SetDelayList([]time.Duration{delay})
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				start := time.Now()
				if iter.DelayContextWithJitter(ctx) {
					t.Fatal("DelayContextWithJitter returned true for canceled context")
				}
				if got := time.Since(start); got != 0 {
					t.Fatalf("canceled delay blocked for %v", got)
				}
			})
		})
	}
	for _, jitter := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			iter := NewDelayIterator()
			iter.SetDelayList([]time.Duration{time.Hour})
			wait := iter.DelayContext
			if jitter {
				wait = iter.DelayContextWithJitter
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			time.AfterFunc(time.Second, cancel)
			start := time.Now()
			if wait(ctx) {
				t.Fatalf("wait returned true after cancellation (jitter = %v)", jitter)
			}
			if got := time.Since(start); got != time.Second {
				t.Fatalf("cancellation took %v, want 1s (jitter = %v)", got, jitter)
			}
		})
	}
}
