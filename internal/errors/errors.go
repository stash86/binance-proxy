package errors

import (
	"errors"
	"fmt"
	"time"
)

// Custom error types for better error handling
type ProxyError struct {
	Type      ErrorType
	Message   string
	Timestamp time.Time
	Cause     error
}

type ErrorType string

const (
	ErrTypeWebSocket      ErrorType = "websocket"
	ErrTypeRateLimit      ErrorType = "ratelimit"
	ErrTypeConfig         ErrorType = "config"
	ErrTypeNetwork        ErrorType = "network"
	ErrTypeDataProcessing ErrorType = "dataprocessing"
	ErrTypeServer         ErrorType = "server"
)

func (e *ProxyError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v (at %s)", e.Type, e.Message, e.Cause, e.Timestamp.Format(time.RFC3339))
	}
	return fmt.Sprintf("[%s] %s (at %s)", e.Type, e.Message, e.Timestamp.Format(time.RFC3339))
}

func (e *ProxyError) Unwrap() error {
	return e.Cause
}

// New creates a new ProxyError
func New(errType ErrorType, message string, cause error) *ProxyError {
	return &ProxyError{
		Type:      errType,
		Message:   message,
		Timestamp: time.Now(),
		Cause:     cause,
	}
}

// Newf creates a new ProxyError with formatted message
func Newf(errType ErrorType, format string, args ...interface{}) *ProxyError {
	return &ProxyError{
		Type:      errType,
		Message:   fmt.Sprintf(format, args...),
		Timestamp: time.Now(),
	}
}

// Common error instances
var (
	// WebSocket specific errors
	ErrWebSocketConnectionFailed = errors.New("websocket connection failed")
	ErrWebSocketNotConnected     = errors.New("websocket not connected")
	ErrWebSocketTimeout          = errors.New("websocket operation timeout")
	ErrWebSocketPingFailed       = errors.New("websocket ping failed")
	ErrWebSocketPongTimeout      = errors.New("websocket pong timeout")
	ErrWebSocketMessageQueueFull = errors.New("websocket message queue full")
	ErrWebSocketReconnectFailed  = errors.New("websocket reconnection failed")
	ErrWebSocketCircuitOpen      = errors.New("websocket circuit breaker open")
	
	// General errors
	ErrRateLimitExceeded        = errors.New("rate limit exceeded")
	ErrInvalidConfiguration     = errors.New("invalid configuration")
	ErrNetworkTimeout           = errors.New("network timeout")
	ErrDataCorrupted            = errors.New("data corrupted")
	ErrServerShutdown           = errors.New("server shutdown")
)
