package handler

import (
	"bytes"
	"sync"
)

// Shared buffer pool for all handlers to reduce memory overhead
var BufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// GetBuffer gets a buffer from the shared pool
func GetBuffer() *bytes.Buffer {
	return BufferPool.Get().(*bytes.Buffer)
}

// PutBuffer returns a buffer to the shared pool after resetting it
func PutBuffer(buf *bytes.Buffer) {
	if buf != nil {
		buf.Reset()
		BufferPool.Put(buf)
	}
}
