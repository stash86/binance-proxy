package handler

import (
	"bytes"
	"sync"
)

const maxPooledBufferCapacity = 1 << 20 // 1 MiB

// Shared buffer pool for all handlers to reduce memory overhead.
var BufferPool = newBytesBufferPool()

func newBytesBufferPool() sync.Pool {
	return sync.Pool{
		New: func() interface{} {
			return &bytes.Buffer{}
		},
	}
}

// GetBuffer gets a reset buffer from the shared pool.
func GetBuffer() *bytes.Buffer {
	return getBuffer(&BufferPool)
}

// PutBuffer returns a buffer to the shared pool after resetting it.
func PutBuffer(buf *bytes.Buffer) {
	putBuffer(&BufferPool, buf)
}

func getBuffer(pool *sync.Pool) *bytes.Buffer {
	if pool == nil {
		return &bytes.Buffer{}
	}

	buf, ok := pool.Get().(*bytes.Buffer)
	if !ok || buf == nil {
		return &bytes.Buffer{}
	}

	buf.Reset()
	return buf
}

func putBuffer(pool *sync.Pool, buf *bytes.Buffer) {
	if pool == nil || buf == nil {
		return
	}
	if buf.Cap() > maxPooledBufferCapacity {
		return
	}

	buf.Reset()
	pool.Put(buf)
}
