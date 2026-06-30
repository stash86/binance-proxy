package service

import (
	"errors"
	"testing"
	"time"
)

func TestStatusTrackerGetStatus(t *testing.T) {
	startTime := time.Now().Add(-time.Minute)
	tracker := newStatusTracker(startTime)
	tracker.RecordRequest()
	tracker.RecordRequest()
	tracker.RecordError(errors.New("upstream failed"))

	status := tracker.GetStatus()
	if status.Service != statusServiceName {
		t.Fatalf("service = %q, want %q", status.Service, statusServiceName)
	}
	if !status.Healthy {
		t.Fatal("healthy = false, want true below unhealthy request threshold")
	}
	if status.StartTime != startTime {
		t.Fatalf("start time = %v, want %v", status.StartTime, startTime)
	}
	if status.Requests != 2 {
		t.Fatalf("requests = %d, want 2", status.Requests)
	}
	if status.Errors != 1 {
		t.Fatalf("errors = %d, want 1", status.Errors)
	}
	if status.ErrorRate != 50 {
		t.Fatalf("error rate = %f, want 50", status.ErrorRate)
	}
	if status.LastError != "upstream failed" {
		t.Fatalf("last error = %q, want upstream failed", status.LastError)
	}
	if status.LastErrorAt == "" {
		t.Fatal("last error timestamp is empty")
	}
	if status.Timestamp.IsZero() {
		t.Fatal("status timestamp is zero")
	}
	if status.Uptime == "" {
		t.Fatal("uptime is empty")
	}
}

func TestStatusTrackerRecordErrorIgnoresNil(t *testing.T) {
	tracker := newStatusTracker(time.Now())
	tracker.RecordRequest()
	tracker.RecordError(nil)

	status := tracker.GetStatus()
	if status.Errors != 0 {
		t.Fatalf("errors = %d, want 0", status.Errors)
	}
	if status.LastError != "" {
		t.Fatalf("last error = %q, want empty", status.LastError)
	}
}

func TestStatusTrackerBecomesUnhealthy(t *testing.T) {
	tracker := newStatusTracker(time.Now())
	for i := 0; i < statusUnhealthyMinRequests+1; i++ {
		tracker.RecordRequest()
	}
	for i := 0; i < 11; i++ {
		tracker.RecordError(errors.New("boom"))
	}

	status := tracker.GetStatus()
	if status.Healthy {
		t.Fatal("healthy = true, want false above unhealthy error threshold")
	}
}

func TestStatusTrackerSetHealthyAndReset(t *testing.T) {
	tracker := newStatusTracker(time.Now().Add(-time.Hour))
	tracker.RecordRequest()
	tracker.RecordError(errors.New("boom"))
	tracker.SetHealthy(false)

	if tracker.GetStatus().Healthy {
		t.Fatal("healthy = true, want false after SetHealthy(false)")
	}

	tracker.Reset()
	status := tracker.GetStatus()
	if !status.Healthy {
		t.Fatal("healthy = false, want true after reset")
	}
	if status.Requests != 0 || status.Errors != 0 {
		t.Fatalf("requests/errors = %d/%d, want 0/0", status.Requests, status.Errors)
	}
	if status.LastError != "" || status.LastErrorAt != "" {
		t.Fatalf("last error = %q at %q, want empty", status.LastError, status.LastErrorAt)
	}
}
