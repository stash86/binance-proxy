package tool

import (
	"context"
	"time"
)

var defaultDelayList = []time.Duration{
	0 * time.Millisecond,
	10 * time.Millisecond,
	100 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
	2000 * time.Millisecond,
	5000 * time.Millisecond,
	10000 * time.Millisecond,
	15000 * time.Millisecond,
	30000 * time.Millisecond,
	60000 * time.Millisecond,
}

type DelayIterator struct {
	index     int
	delayList []time.Duration
}

func NewDelayIterator() *DelayIterator {
	return &DelayIterator{
		delayList: append([]time.Duration(nil), defaultDelayList...),
	}
}

func (s *DelayIterator) SetDelayList(delayList []time.Duration) {
	if len(delayList) == 0 {
		s.delayList = append([]time.Duration(nil), defaultDelayList...)
	} else {
		s.delayList = append([]time.Duration(nil), delayList...)
	}
	s.Reset()
}

func (s *DelayIterator) Reset() {
	s.index = 0
}

func (s *DelayIterator) Delay() {
	time.Sleep(s.nextDelay())
}

func (s *DelayIterator) DelayContext(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	delay := s.nextDelay()
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *DelayIterator) nextDelay() time.Duration {
	if len(s.delayList) == 0 {
		s.delayList = append([]time.Duration(nil), defaultDelayList...)
	}

	if s.index >= len(s.delayList) {
		return s.delayList[len(s.delayList)-1]
	}

	delay := s.delayList[s.index]
	s.index++
	return delay
}
