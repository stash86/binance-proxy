package handler

import (
	"bytes"
	"sync"
	"testing"
)

func TestGetBufferReturnsResetBuffer(t *testing.T) {
	pool := sync.Pool{
		New: func() interface{} {
			return bytes.NewBufferString("dirty")
		},
	}

	got := getBuffer(&pool)
	if got == nil {
		t.Fatal("getBuffer() returned nil")
	}
	if got.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", got.Len())
	}
}

func TestGetBufferHandlesNilPool(t *testing.T) {
	got := getBuffer(nil)
	if got == nil {
		t.Fatal("getBuffer(nil) returned nil")
	}
	if got.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", got.Len())
	}
}

func TestGetBufferHandlesUnexpectedPoolValue(t *testing.T) {
	pool := sync.Pool{}
	pool.Put("not a buffer")

	got := getBuffer(&pool)
	if got == nil {
		t.Fatal("getBuffer() returned nil")
	}
	if got.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", got.Len())
	}
}

func TestPutBufferResetsSmallBuffer(t *testing.T) {
	pool := newBytesBufferPool()
	buf := bytes.NewBufferString("payload")

	putBuffer(&pool, buf)

	if buf.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", buf.Len())
	}
}

func TestPutBufferDropsLargeBuffer(t *testing.T) {
	pool := newBytesBufferPool()
	large := bytes.NewBuffer(make([]byte, maxPooledBufferCapacity+1))

	putBuffer(&pool, large)
	got := getBuffer(&pool)

	if got == large {
		t.Fatal("large buffer was returned to pool")
	}
	if got.Cap() > maxPooledBufferCapacity {
		t.Fatalf("buffer cap = %d, want <= %d", got.Cap(), maxPooledBufferCapacity)
	}
}

func TestPutBufferHandlesNilInputs(t *testing.T) {
	putBuffer(nil, bytes.NewBufferString("payload"))
	putBuffer(&sync.Pool{}, nil)
	PutBuffer(nil)
}

func TestPublicBufferPoolRoundTrip(t *testing.T) {
	buf := GetBuffer()
	if buf == nil {
		t.Fatal("GetBuffer() returned nil")
	}

	buf.WriteString("payload")
	PutBuffer(buf)

	if buf.Len() != 0 {
		t.Fatalf("buffer len = %d, want 0", buf.Len())
	}
}
