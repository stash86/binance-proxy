package websocket

import (
	"binance-proxy/internal/config"
	"binance-proxy/internal/errors"
	"binance-proxy/internal/metrics"
	"binance-proxy/internal/recovery"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// ConnectionState represents the current state of a WebSocket connection
type ConnectionState int32

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	StateFailed
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ConnectionInfo holds metadata about a WebSocket connection
type ConnectionInfo struct {
	ID            string
	URL           string
	Symbol        string
	Interval      string
	ConnectedAt   time.Time
	LastMessage   time.Time
	MessageCount  int64
	ErrorCount    int64
	State         ConnectionState
	LastError     error
	ReconnectCount int64
}

// MessageHandler defines the interface for handling WebSocket messages
type MessageHandler interface {
	HandleMessage(data []byte) error
	HandleError(err error)
	HandleConnect()
	HandleDisconnect()
}

// Manager manages WebSocket connections with enhanced features
type Manager struct {
	config      *config.WebSocketConfig
	metrics     *metrics.Metrics
	recovery    *recovery.Recovery
	connections map[string]*Connection
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// Connection represents an enhanced WebSocket connection
type Connection struct {
	ID              string
	URL             string
	Symbol          string
	Interval        string
	conn            *websocket.Conn
	handler         MessageHandler
	manager         *Manager
	state           int32 // atomic access to ConnectionState
	connectedAt     time.Time
	lastMessage     time.Time
	messageCount    int64
	errorCount      int64
	reconnectCount  int64
	lastError       error
	ctx             context.Context
	cancel          context.CancelFunc
	writeMu         sync.Mutex
	readMu          sync.Mutex
	pingTicker      *time.Ticker
	pongReceived    chan struct{}
	reconnectDelay  time.Duration
	maxReconnects   int
	circuitBreaker  *recovery.CircuitBreaker
}

// NewManager creates a new WebSocket manager
func NewManager(cfg *config.WebSocketConfig, m *metrics.Metrics, r *recovery.Recovery) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &Manager{
		config:      cfg,
		metrics:     m,
		recovery:    r,
		connections: make(map[string]*Connection),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Connect creates a new WebSocket connection with enhanced features
func (m *Manager) Connect(id, url, symbol, interval string, handler MessageHandler) (*Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if connection already exists
	if existing, exists := m.connections[id]; exists {
		log.Warnf("WebSocket connection %s already exists, closing old connection", id)
		existing.Close()
	}

	// Create circuit breaker for this connection
	cb := m.recovery.NewCircuitBreaker(fmt.Sprintf("websocket-%s", id), 10, 30*time.Second)

	ctx, cancel := context.WithCancel(m.ctx)
	conn := &Connection{
		ID:             id,
		URL:            url,
		Symbol:         symbol,
		Interval:       interval,
		handler:        handler,
		manager:        m,
		ctx:            ctx,
		cancel:         cancel,
		pongReceived:   make(chan struct{}, 1),
		reconnectDelay: time.Second,
		maxReconnects:  m.config.MaxReconnects,
		circuitBreaker: cb,
	}

	atomic.StoreInt32(&conn.state, int32(StateConnecting))

	// Start connection in background
	go conn.connect()

	m.connections[id] = conn
	return conn, nil
}

// GetConnection retrieves a connection by ID
func (m *Manager) GetConnection(id string) (*Connection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, exists := m.connections[id]
	return conn, exists
}

// GetAllConnections returns all active connections
func (m *Manager) GetAllConnections() map[string]*ConnectionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info := make(map[string]*ConnectionInfo)
	for id, conn := range m.connections {
		info[id] = &ConnectionInfo{
			ID:             conn.ID,
			URL:            conn.URL,
			Symbol:         conn.Symbol,
			Interval:       conn.Interval,
			ConnectedAt:    conn.connectedAt,
			LastMessage:    conn.lastMessage,
			MessageCount:   atomic.LoadInt64(&conn.messageCount),
			ErrorCount:     atomic.LoadInt64(&conn.errorCount),
			State:          ConnectionState(atomic.LoadInt32(&conn.state)),
			LastError:      conn.lastError,
			ReconnectCount: atomic.LoadInt64(&conn.reconnectCount),
		}
	}
	return info
}

// Shutdown gracefully closes all connections
func (m *Manager) Shutdown(timeout time.Duration) error {
	log.Info("WebSocket manager shutting down...")
	
	m.cancel()
	
	done := make(chan struct{})
	go func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		
		for _, conn := range m.connections {
			conn.Close()
		}
		close(done)
	}()

	select {
	case <-done:
		log.Info("WebSocket manager shutdown completed")
		return nil
	case <-time.After(timeout):
		log.Warn("WebSocket manager shutdown timed out")
		return fmt.Errorf("shutdown timeout after %v", timeout)
	}
}

// connect establishes the WebSocket connection
func (c *Connection) connect() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("WebSocket connection %s panic: %v", c.ID, r)
			atomic.StoreInt32(&c.state, int32(StateFailed))
		}
	}()

	// Check circuit breaker
	if !c.circuitBreaker.Allow() {
		log.Warnf("WebSocket connection %s blocked by circuit breaker", c.ID)
		atomic.StoreInt32(&c.state, int32(StateFailed))
		return
	}

	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: c.manager.config.HandshakeTimeout,
		ReadBufferSize:   c.manager.config.BufferSize,
		WriteBufferSize:  c.manager.config.BufferSize,
	}

	headers := http.Header{}
	headers.Set("User-Agent", "binance-proxy/2.0")

	log.Debugf("WebSocket connecting to %s for %s", c.URL, c.ID)
	
	conn, _, err := dialer.Dial(c.URL, headers)
	if err != nil {
		atomic.AddInt64(&c.errorCount, 1)
		c.lastError = err
		atomic.StoreInt32(&c.state, int32(StateFailed))
		c.circuitBreaker.RecordFailure()
		c.manager.metrics.IncrementWebSocketError()
		log.Errorf("WebSocket connection %s failed: %v", c.ID, err)
		
		// Schedule reconnect
		c.scheduleReconnect()
		return
	}

	c.conn = conn
	c.connectedAt = time.Now()
	atomic.StoreInt32(&c.state, int32(StateConnected))
	c.circuitBreaker.RecordSuccess()
	c.manager.metrics.IncrementWebSocketConnection()

	log.Infof("WebSocket connection %s established", c.ID)
	c.handler.HandleConnect()

	// Start ping/pong mechanism
	c.startPingPong()

	// Start message readers
	go c.readMessages()
	go c.handlePingPong()
}

// startPingPong initializes the ping/pong mechanism
func (c *Connection) startPingPong() {
	c.pingTicker = time.NewTicker(c.manager.config.PingInterval)
	
	// Set pong handler
	c.conn.SetPongHandler(func(string) error {
		select {
		case c.pongReceived <- struct{}{}:
		default:
		}
		return nil
	})
}

// handlePingPong manages ping/pong heartbeat
func (c *Connection) handlePingPong() {
	defer c.pingTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.pingTicker.C:
			c.writeMu.Lock()
			if c.conn != nil {
				c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					c.writeMu.Unlock()
					log.Warnf("WebSocket %s ping failed: %v", c.ID, err)
					c.reconnect()
					return
				}
			}
			c.writeMu.Unlock()

			// Wait for pong with timeout
			select {
			case <-c.pongReceived:
				// Pong received, continue
			case <-time.After(c.manager.config.PongTimeout):
				log.Warnf("WebSocket %s pong timeout", c.ID)
				c.reconnect()
				return
			case <-c.ctx.Done():
				return
			}
		}
	}
}

// readMessages reads messages from the WebSocket connection
func (c *Connection) readMessages() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("WebSocket %s read panic: %v", c.ID, r)
		}
		c.reconnect()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.readMu.Lock()
		if c.conn == nil {
			c.readMu.Unlock()
			return
		}

		// Set read deadline
		c.conn.SetReadDeadline(time.Now().Add(c.manager.config.PongTimeout))
		
		messageType, data, err := c.conn.ReadMessage()
		c.readMu.Unlock()

		if err != nil {
			atomic.AddInt64(&c.errorCount, 1)
			c.lastError = err
			
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Errorf("WebSocket %s unexpected close: %v", c.ID, err)
			} else {
				log.Debugf("WebSocket %s read error: %v", c.ID, err)
			}
			return
		}

		if messageType == websocket.TextMessage {
			atomic.AddInt64(&c.messageCount, 1)
			c.lastMessage = time.Now()
			c.manager.metrics.IncrementWebSocketMessage()

			// Handle message in background to avoid blocking
			go func(data []byte) {
				if err := c.handler.HandleMessage(data); err != nil {
					log.Errorf("WebSocket %s message handler error: %v", c.ID, err)
					atomic.AddInt64(&c.errorCount, 1)
				}
			}(data)
		}
	}
}

// reconnect handles connection reconnection with exponential backoff
func (c *Connection) reconnect() {
	currentState := atomic.LoadInt32(&c.state)
	if currentState == int32(StateReconnecting) || currentState == int32(StateDisconnected) {
		return // Already reconnecting or disconnected
	}

	atomic.StoreInt32(&c.state, int32(StateReconnecting))
	atomic.AddInt64(&c.reconnectCount, 1)
	
	log.Infof("WebSocket %s reconnecting (attempt %d)", c.ID, atomic.LoadInt64(&c.reconnectCount))
	
	// Close existing connection
	c.closeConnection()
	
	// Check if we've exceeded max reconnects
	if int(atomic.LoadInt64(&c.reconnectCount)) > c.maxReconnects {
		log.Errorf("WebSocket %s exceeded max reconnection attempts", c.ID)
		atomic.StoreInt32(&c.state, int32(StateFailed))
		return
	}

	c.scheduleReconnect()
}

// scheduleReconnect schedules a reconnection attempt with exponential backoff
func (c *Connection) scheduleReconnect() {
	delay := c.manager.recovery.CalculateDelay(int(atomic.LoadInt64(&c.reconnectCount)))
	
	log.Debugf("WebSocket %s scheduling reconnect in %v", c.ID, delay)
	
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		c.connect()
	case <-c.ctx.Done():
		return
	}
}

// closeConnection safely closes the WebSocket connection
func (c *Connection) closeConnection() {
	c.writeMu.Lock()
	c.readMu.Lock()
	defer c.writeMu.Unlock()
	defer c.readMu.Unlock()

	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
		c.manager.metrics.DecrementWebSocketConnection()
		c.handler.HandleDisconnect()
	}

	if c.pingTicker != nil {
		c.pingTicker.Stop()
	}
}

// Close gracefully closes the connection
func (c *Connection) Close() {
	log.Debugf("WebSocket %s closing", c.ID)
	
	atomic.StoreInt32(&c.state, int32(StateDisconnected))
	c.cancel()
	c.closeConnection()

	// Remove from manager
	c.manager.mu.Lock()
	delete(c.manager.connections, c.ID)
	c.manager.mu.Unlock()
}

// SendMessage sends a message to the WebSocket connection
func (c *Connection) SendMessage(message interface{}) error {
	if atomic.LoadInt32(&c.state) != int32(StateConnected) {
		return errors.ErrWebSocketNotConnected
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if c.conn == nil {
		return errors.ErrWebSocketNotConnected
	}

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// GetState returns the current connection state
func (c *Connection) GetState() ConnectionState {
	return ConnectionState(atomic.LoadInt32(&c.state))
}

// Stats represents WebSocket statistics
type Stats struct {
	TotalConnections    int64                  `json:"total_connections"`
	ActiveConnections   int64                  `json:"active_connections"`
	TotalMessages       int64                  `json:"total_messages"`
	TotalErrors         int64                  `json:"total_errors"`
	TotalReconnects     int64                  `json:"total_reconnects"`
	ConnectionsByState  map[string]int         `json:"connections_by_state"`
	AverageLatency      float64                `json:"average_latency_ms"`
}

// Manager manages multiple WebSocket connections
type Manager struct {
	connections map[string]*Connection
	mu          sync.RWMutex
	config      *Config
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewManager creates a new WebSocket manager
func NewManager(config interface{}) *Manager {
	// This would need proper config interface, but for now we'll use a basic implementation
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		connections: make(map[string]*Connection),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Close closes all connections and stops the manager
func (m *Manager) Close() error {
	m.cancel()
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	for _, conn := range m.connections {
		conn.Close()
	}
	
	return nil
}

// IsHealthy returns whether the WebSocket manager is healthy
func (m *Manager) IsHealthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// Consider healthy if at least some connections are connected
	connected := 0
	for _, conn := range m.connections {
		if conn.GetState() == StateConnected {
			connected++
		}
	}
	
	return len(m.connections) == 0 || connected > 0
}

// GetStats returns WebSocket statistics
func (m *Manager) GetStats() *Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	stats := &Stats{
		TotalConnections:   int64(len(m.connections)),
		ConnectionsByState: make(map[string]int),
	}
	
	var totalMessages, totalErrors, totalReconnects int64
	var activeConnections int64
	
	for _, conn := range m.connections {
		connStats := conn.GetStats()
		
		// Type assertions with safety checks
		if msgCount, ok := connStats["message_count"].(int64); ok {
			totalMessages += msgCount
		}
		if errCount, ok := connStats["error_count"].(int64); ok {
			totalErrors += errCount
		}
		if reconCount, ok := connStats["reconnect_count"].(int64); ok {
			totalReconnects += reconCount
		}
		
		state := conn.GetState().String()
		stats.ConnectionsByState[state]++
		
		if conn.GetState() == StateConnected {
			activeConnections++
		}
	}
	
	stats.TotalMessages = totalMessages
	stats.TotalErrors = totalErrors
	stats.TotalReconnects = totalReconnects
	stats.ActiveConnections = activeConnections
	
	return stats
}

// GetStats returns connection statistics
func (c *Connection) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"id":               c.ID,
		"url":              c.URL,
		"symbol":           c.Symbol,
		"interval":         c.Interval,
		"state":            c.GetState().String(),
		"connected_at":     c.connectedAt,
		"last_message":     c.lastMessage,
		"message_count":    atomic.LoadInt64(&c.messageCount),
		"error_count":      atomic.LoadInt64(&c.errorCount),
		"reconnect_count":  atomic.LoadInt64(&c.reconnectCount),
		"last_error":       c.lastError,
	}
}
