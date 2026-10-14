//go:build (linux || darwin) && !windows

package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tlslocal"
)

func TestWrapTCPListenerWithTLS_NilSafe(t *testing.T) {
	wrapTCPListenerWithTLS(nil, &tls.Config{MinVersion: tls.VersionTLS12})
	wrapTCPListenerWithTLS(&Listener{}, nil)
}

// TestTLSEnabledKeepsTraySocketPlainHTTP reproduces issue #1623: when TLS is
// enabled, the TCP listener must require TLS while the tray Unix socket must
// still accept plain HTTP (tray/CLI speak cleartext over local IPC).
func TestTLSEnabledKeepsTraySocketPlainHTTP(t *testing.T) {
	dataDir, err := os.MkdirTemp("/tmp", "mcpx-tls-dual")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	require.NoError(t, os.Chmod(dataDir, 0o700))

	certsDir := filepath.Join(dataDir, "certs")
	tlsCfg, err := tlslocal.EnsureServerTLSConfig(tlslocal.Options{Dir: certsDir})
	require.NoError(t, err)

	logger := zap.NewNop()
	manager := NewListenerManager(&ListenerConfig{
		DataDir:    dataDir,
		TCPAddress: "127.0.0.1:0",
		Logger:     logger,
	})
	tcpListener, err := manager.CreateTCPListener()
	require.NoError(t, err)
	require.NotNil(t, tcpListener)

	trayListener, err := manager.CreateTrayListener()
	require.NoError(t, err)
	require.NotNil(t, trayListener)

	// Production path for tls.enabled=true: wrap TCP only, then Serve the mux.
	wrapTCPListenerWithTLS(tcpListener, tlsCfg)

	muxLn := &multiplexListener{
		listeners: []*Listener{tcpListener, trayListener},
		logger:    logger,
	}

	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		}),
		// Short header timeout so a plain-HTTP probe against the TLS TCP port
		// does not pin a server goroutine through test cleanup.
		ReadHeaderTimeout: 500 * time.Millisecond,
		ConnContext:       taggedConnContext,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(muxLn) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		_ = muxLn.Close()
		_ = manager.CloseAll()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	})

	socketPath := trayListener.Address
	tcpAddr := tcpListener.Addr().String()

	// Tray/CLI path: plain HTTP over the Unix socket must succeed.
	plainSocketClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}
	resp, err := plainSocketClient.Get("http://localhost/ready")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "plain HTTP over tray socket: %s", body)

	// TCP without TLS must fail (server expects a TLS handshake).
	plainTCPClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	_, err = plainTCPClient.Get(fmt.Sprintf("http://%s/ready", tcpAddr))
	require.Error(t, err, "plain HTTP over TLS TCP listener should fail")

	// Give the mis-spoken TCP conn time to hit ReadHeaderTimeout and leave.
	time.Sleep(700 * time.Millisecond)

	// TCP with TLS must succeed.
	tlsClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // local ephemeral CA
				MinVersion:         tls.VersionTLS12,
			},
		},
	}
	resp, err = tlsClient.Get(fmt.Sprintf("https://%s/ready", tcpAddr))
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "HTTPS over TCP: %s", body)
}
