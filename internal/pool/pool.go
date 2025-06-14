package pool

import (
	"sync"
	"time"
)

// BufferPool manages reusable byte buffers for WebSocket operations
type BufferPool struct {
	pool        sync.Pool
	bufferSize  int
	maxBuffers  int
	activeCount int64
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(bufferSize, maxBuffers int) *BufferPool {
	return &BufferPool{
		bufferSize: bufferSize,
		maxBuffers: maxBuffers,
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, bufferSize)
			},
		},
	}
}

// Get retrieves a buffer from the pool
func (p *BufferPool) Get() []byte {
	return p.pool.Get().([]byte)
}

// Put returns a buffer to the pool
func (p *BufferPool) Put(buf []byte) {
	if len(buf) == p.bufferSize {
		// Clear the buffer before returning to pool
		for i := range buf {
			buf[i] = 0
		}
		p.pool.Put(buf)
	}
}

// ConnectionPool manages WebSocket connection objects for reuse
type ConnectionPool struct {
	pool        sync.Pool
	maxSize     int
	activeConns int64
}

// ConnectionWrapper wraps connection data for reuse
type ConnectionWrapper struct {
	Buffer      []byte
	LastUsed    time.Time
	InUse       bool
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(bufferSize, maxSize int) *ConnectionPool {
	return &ConnectionPool{
		maxSize: maxSize,
		pool: sync.Pool{
			New: func() interface{} {
				return &ConnectionWrapper{
					Buffer:   make([]byte, bufferSize),
					LastUsed: time.Now(),
					InUse:    false,
				}
			},
		},
	}
}

// Get retrieves a connection wrapper from the pool
func (p *ConnectionPool) Get() *ConnectionWrapper {
	wrapper := p.pool.Get().(*ConnectionWrapper)
	wrapper.LastUsed = time.Now()
	wrapper.InUse = true
	return wrapper
}

// Put returns a connection wrapper to the pool
func (p *ConnectionPool) Put(wrapper *ConnectionWrapper) {
	if wrapper != nil {
		wrapper.InUse = false
		wrapper.LastUsed = time.Now()
		
		// Clear sensitive data
		if wrapper.Buffer != nil {
			for i := range wrapper.Buffer {
				wrapper.Buffer[i] = 0
			}
		}
		
		p.pool.Put(wrapper)
	}
}

// StringPool manages string interning to reduce memory usage
type StringPool struct {
	mu      sync.RWMutex
	strings map[string]string
	maxSize int
}

// NewStringPool creates a new string pool for interning
func NewStringPool(maxSize int) *StringPool {
	return &StringPool{
		strings: make(map[string]string, maxSize),
		maxSize: maxSize,
	}
}

// Intern returns an interned version of the string to save memory
func (p *StringPool) Intern(s string) string {
	p.mu.RLock()
	if interned, exists := p.strings[s]; exists {
		p.mu.RUnlock()
		return interned
	}
	p.mu.RUnlock()
	
	p.mu.Lock()
	defer p.mu.Unlock()
	
	// Double-check after acquiring write lock
	if interned, exists := p.strings[s]; exists {
		return interned
	}
	
	// Add to pool if not full
	if len(p.strings) < p.maxSize {
		p.strings[s] = s
		return s
	}
	
	// Pool is full, return original string
	return s
}

// Clear clears the string pool
func (p *StringPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	p.strings = make(map[string]string, p.maxSize)
}

// Size returns the current size of the string pool
func (p *StringPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.strings)
}

// Global pools
var (
	defaultBufferPool     *BufferPool
	defaultConnectionPool *ConnectionPool
	defaultStringPool     *StringPool
	poolOnce              sync.Once
)

// InitializePools initializes the global pools
func InitializePools() {
	poolOnce.Do(func() {
		defaultBufferPool = NewBufferPool(4096, 100)       // 4KB buffers
		defaultConnectionPool = NewConnectionPool(8192, 50) // 8KB connection buffers
		defaultStringPool = NewStringPool(1000)             // 1000 interned strings
	})
}

// GetBuffer gets a buffer from the default pool
func GetBuffer() []byte {
	if defaultBufferPool == nil {
		InitializePools()
	}
	return defaultBufferPool.Get()
}

// PutBuffer returns a buffer to the default pool
func PutBuffer(buf []byte) {
	if defaultBufferPool != nil {
		defaultBufferPool.Put(buf)
	}
}

// GetConnectionWrapper gets a connection wrapper from the default pool
func GetConnectionWrapper() *ConnectionWrapper {
	if defaultConnectionPool == nil {
		InitializePools()
	}
	return defaultConnectionPool.Get()
}

// PutConnectionWrapper returns a connection wrapper to the default pool
func PutConnectionWrapper(wrapper *ConnectionWrapper) {
	if defaultConnectionPool != nil {
		defaultConnectionPool.Put(wrapper)
	}
}

// InternString interns a string using the default pool
func InternString(s string) string {
	if defaultStringPool == nil {
		InitializePools()
	}
	return defaultStringPool.Intern(s)
}
