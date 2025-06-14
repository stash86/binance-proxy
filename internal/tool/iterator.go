package tool

import (
	"math"
	"math/rand"
	"time"

	log "github.com/sirupsen/logrus"
)

type DelayIterator struct {
	index     int
	delayList []time.Duration
	maxDelay  time.Duration
	baseDelay time.Duration
	useExponentialBackoff bool
}

func NewDelayIterator() *DelayIterator {
	return &DelayIterator{
		delayList: []time.Duration{
			0 * time.Millisecond,
			100 * time.Millisecond,
			200 * time.Millisecond,
			500 * time.Millisecond,
			1000 * time.Millisecond,
			2000 * time.Millisecond,
			5000 * time.Millisecond,
			10000 * time.Millisecond,
			15000 * time.Millisecond,
			30000 * time.Millisecond,
			60000 * time.Millisecond,
		},
		maxDelay:  60 * time.Second,
		baseDelay: 100 * time.Millisecond,
		useExponentialBackoff: false,
	}
}

func NewExponentialBackoffIterator(baseDelay, maxDelay time.Duration) *DelayIterator {
	return &DelayIterator{
		baseDelay: baseDelay,
		maxDelay:  maxDelay,
		useExponentialBackoff: true,
	}
}

func (s *DelayIterator) SetDelayList(delayList []time.Duration) {
	s.delayList = delayList
	s.useExponentialBackoff = false
}

func (s *DelayIterator) SetExponentialBackoff(baseDelay, maxDelay time.Duration) {
	s.baseDelay = baseDelay
	s.maxDelay = maxDelay
	s.useExponentialBackoff = true
}

func (s *DelayIterator) Reset() {
	s.index = 0
}

func (s *DelayIterator) Delay() {
	var delay time.Duration
	
	if s.useExponentialBackoff {
		// Exponential backoff with jitter
		delay = time.Duration(float64(s.baseDelay) * math.Pow(2, float64(s.index)))
		if delay > s.maxDelay {
			delay = s.maxDelay
		}
		
		// Add jitter (±25%)
		jitter := time.Duration(rand.Float64() * 0.5 * float64(delay))
		if rand.Float64() < 0.5 {
			delay -= jitter
		} else {
			delay += jitter
		}
		
		s.index++
	} else {
		// Use predefined delay list
		if s.index >= len(s.delayList) {
			delay = s.delayList[len(s.delayList)-1]
		} else {
			delay = s.delayList[s.index]
			s.index++
		}
	}
	
	if delay > 0 {
		log.Debugf("Delaying reconnection for %v", delay)
		time.Sleep(delay)
	}
}

// GetCurrentDelay returns the current delay without sleeping
func (s *DelayIterator) GetCurrentDelay() time.Duration {
	if s.useExponentialBackoff {
		delay := time.Duration(float64(s.baseDelay) * math.Pow(2, float64(s.index)))
		if delay > s.maxDelay {
			delay = s.maxDelay
		}
		return delay
	}
	
	if s.index >= len(s.delayList) {
		return s.delayList[len(s.delayList)-1]
	}
	return s.delayList[s.index]
}
