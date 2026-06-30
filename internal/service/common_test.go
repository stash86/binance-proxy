package service

import (
	"testing"
	"time"
)

func TestIntervalDuration(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     time.Duration
		wantOK   bool
	}{
		{name: "seconds", interval: "1s", want: time.Second, wantOK: true},
		{name: "minutes", interval: "15m", want: 15 * time.Minute, wantOK: true},
		{name: "months", interval: "1M", want: 31 * 24 * time.Hour, wantOK: true},
		{name: "unknown", interval: "2m", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := intervalDuration(tt.interval)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}
