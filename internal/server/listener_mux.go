package server

import (
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"
)

// multiplexListener accepts connections from multiple underlying listeners
// It allows serving the same HTTP handler on both TCP and Unix socket/named pipe
type multiplexListener struct {
	listeners []*Listener
	logger    *zap.Logger
	connCh    chan net.Conn
	errCh     chan error
	once      sync.Once
	closeOnce sync.Once
	closed    bool
	mu        sync.RWMutex
}

// Accept waits for and returns the next connection from any underlying listener
func (m *multiplexListener) Accept() (net.Conn, error) {
	// Start accepting goroutines on first call
	m.once.Do(func() {
		m.connCh = make(chan net.Conn)
		m.errCh = make(chan error, len(m.listeners))

		// Start accept loop for each listener
		for _, ln := range m.listeners {
			go m.acceptLoop(ln)
		}
	})

	select {
	case conn := <-m.connCh:
		return conn, nil
	case err := <-m.errCh:
		return nil, err
	}
}

// acceptLoop continuously accepts connections from a single listener
func (m *multiplexListener) acceptLoop(ln *Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			m.mu.RLock()
			closed := m.closed
			m.mu.RUnlock()

			if closed {
				return // Expected error during shutdown
			}

			m.logger.Error("Listener accept error",
				zap.Error(err),
				zap.String("source", string(ln.Source)),
				zap.String("address", ln.Address))

			m.errCh <- err
			return
		}

		// Wrap connection with source tag
		taggedConn := &taggedConn{
			Conn:   conn,
			source: ln.Source,
		}

		m.connCh <- taggedConn
	}
}

// Close closes all underlying listeners
func (m *multiplexListener) Close() error {
	var firstErr error

	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()

		for _, ln := range m.listeners {
			if err := ln.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}

		// Close channels
		if m.connCh != nil {
			close(m.connCh)
		}
		if m.errCh != nil {
			close(m.errCh)
		}
	})

	return firstErr
}

// Addr returns the address of the first listener (for compatibility)
func (m *multiplexListener) Addr() net.Addr {
	if len(m.listeners) > 0 && m.listeners[0] != nil {
		return m.listeners[0].Addr()
	}
	return &net.TCPAddr{}
}

// taggedConn wraps a net.Conn with connection source information
type taggedConn struct {
	net.Conn
	source ConnectionSource
}

// PermissionError represents a permission-related error (exit code 5)
type PermissionError struct {
	Path string
	Err  error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("permission error for %s: %v", e.Path, e.Err)
}

func (e *PermissionError) Unwrap() error {
	return e.Err
}
