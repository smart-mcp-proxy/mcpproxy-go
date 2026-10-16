package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"
)

// multiplexListener accepts connections from multiple underlying listeners
// It allows serving the same HTTP handler on both TCP and Unix socket/named pipe
//
// Shutdown: Close closes done, closes the underlying listeners and waits for
// every acceptLoop to exit. connCh and errCh are never closed, so an acceptLoop
// that races Close can never send on a closed channel; its sends select on done
// instead, and a connection it cannot deliver is closed. Accept returns
// net.ErrClosed once done is closed.
type multiplexListener struct {
	listeners []*Listener
	logger    *zap.Logger
	connCh    chan net.Conn
	errCh     chan error
	done      chan struct{}
	initOnce  sync.Once
	closeOnce sync.Once
	closed    bool // guarded by mu
	started   bool // guarded by mu
	mu        sync.RWMutex
	loops     sync.WaitGroup
}

// init allocates the channels. The mux is built as a struct literal, so this
// runs lazily from both Accept and Close.
func (m *multiplexListener) init() {
	m.initOnce.Do(func() {
		m.connCh = make(chan net.Conn)
		m.errCh = make(chan error, len(m.listeners))
		m.done = make(chan struct{})
	})
}

// Accept waits for and returns the next connection from any underlying listener
func (m *multiplexListener) Accept() (net.Conn, error) {
	m.init()

	// Start accepting goroutines on first call, unless already closed
	m.mu.Lock()
	if !m.started && !m.closed {
		m.started = true
		for _, ln := range m.listeners {
			m.loops.Add(1)
			go m.acceptLoop(ln)
		}
	}
	m.mu.Unlock()

	// Prefer shutdown over a connection that raced it.
	select {
	case <-m.done:
		return nil, net.ErrClosed
	default:
	}

	select {
	case conn := <-m.connCh:
		return conn, nil
	case err := <-m.errCh:
		return nil, err
	case <-m.done:
		return nil, net.ErrClosed
	}
}

// acceptLoop continuously accepts connections from a single listener
func (m *multiplexListener) acceptLoop(ln *Listener) {
	defer m.loops.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-m.done:
				return // Expected error during shutdown
			default:
			}

			m.logger.Error("Listener accept error",
				zap.Error(err),
				zap.String("source", string(ln.Source)),
				zap.String("address", ln.Address))

			select {
			case m.errCh <- err:
			case <-m.done:
			}
			return
		}

		// Wrap connection with source tag
		var tagged net.Conn = &taggedConn{
			Conn:   conn,
			source: ln.Source,
		}
		// TLS goes outside the tag so net/http sees a *tls.Conn (r.TLS, h2).
		// The handshake runs lazily in the connection's own goroutine.
		if ln.tlsConfig != nil {
			tagged = tls.Server(tagged, ln.tlsConfig)
		}

		select {
		case m.connCh <- tagged:
		case <-m.done:
			// Accepted as the mux closed: nobody will serve it.
			_ = tagged.Close()
			return
		}
	}
}

// Close closes all underlying listeners and waits for the accept loops to exit
func (m *multiplexListener) Close() error {
	var firstErr error

	m.closeOnce.Do(func() {
		m.init()

		m.mu.Lock()
		m.closed = true
		close(m.done)
		m.mu.Unlock()

		for _, ln := range m.listeners {
			if err := ln.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}

		m.loops.Wait()
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

// taggedConnContext is the http.Server ConnContext hook that carries a
// connection's source into every request context served over it. It is the ONLY
// place the transport identity enters the request path: the API-key middleware
// grants an admin auth context to tray (Unix socket / named pipe) connections on
// its strength, so the admin write gate ultimately rests on this hook.
//
// Named (rather than inlined at the http.Server literal) so an integration test
// can serve the real handler over a real socket through the exact production
// tagging, instead of a look-alike copy that could drift from it.
func taggedConnContext(ctx context.Context, c net.Conn) context.Context {
	// A TLS listener wraps the tagged connection; look through it.
	if tlsConn, ok := c.(*tls.Conn); ok {
		c = tlsConn.NetConn()
	}
	// Extract source from tagged connection
	if tc, ok := c.(*taggedConn); ok {
		return TagConnectionContext(ctx, tc.source)
	}
	return TagConnectionContext(ctx, ConnectionSourceTCP) // Default to TCP
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
