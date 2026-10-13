package core

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	uptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// GH #1537: a no-auth upstream ended up parked in Pending Auth forever after a
// network outage. The DNS error text quoted an ephemeral port containing
// "401", the substring classifiers read it as an OAuth challenge, the ladder
// fell through to the OAuth strategy, and an empty token store turned that
// into a permanent sign-in prompt.

// dnsOutageErr builds the error chain tryNoAuth returns for a DNS timeout:
// tryNoAuth's wrapper → mcp-go transport.Error → "failed to send request" →
// *url.Error → *net.OpError → *net.DNSError. The resolver's ephemeral port
// (54012) contains "401".
func dnsOutageErr() error {
	dns := &net.DNSError{
		Name:      "docs.mcp.cloudflare.com",
		Server:    "192.168.1.1:53",
		Err:       "read udp 192.168.1.20:54012->192.168.1.1:53: i/o timeout",
		IsTimeout: true,
	}
	op := &net.OpError{Op: "dial", Net: "tcp", Err: dns}
	ue := &url.Error{Op: "Post", URL: "https://docs.mcp.cloudflare.com/mcp", Err: op}
	sent := fmt.Errorf("failed to send request: %w", ue)
	return fmt.Errorf("MCP initialize failed during no-auth strategy: %w", uptransport.NewError(sent))
}

func TestIsNetworkError(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"DNS timeout with 401 in the port", dnsOutageErr(), true},
		{"connection refused", fmt.Errorf("wrapped: %w", refused), true},
		{"bare ECONNREFUSED", fmt.Errorf("x: %w", syscall.ECONNREFUSED), true},
		{"deadline exceeded", fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		{"typed 401", fmt.Errorf("MCP initialize failed during no-auth strategy: %w", &uptransport.AuthorizationRequiredError{}), false},
		{"typed 401 beside a net error", errors.Join(&uptransport.AuthorizationRequiredError{}, refused), false},
		// No HTTP response at all: a *url.Error from http.Client.Do. The URL
		// text (port 4010) must not matter.
		{"EOF before any response", &url.Error{Op: "Post", URL: "http://127.0.0.1:4010/mcp", Err: io.EOF}, true},
		{"TLS verification failure", fmt.Errorf("x: %w", &url.Error{Op: "Post", URL: "https://h:4030/mcp", Err: x509.UnknownAuthorityError{}}), true},
		{"bare io.EOF", fmt.Errorf("x: %w", io.EOF), true},
		{"string unauthorized", errors.New("unauthorized"), false},
		{"string 403", errors.New("request failed with status 403: Forbidden"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsNetworkError(tc.err))
		})
	}
}

func TestRunAuthStrategies_NetworkErrorWith401DigitsDoesNotReachOAuth(t *testing.T) {
	c := newStrategyTestClient()
	outage := dnsOutageErr()
	// The trap this guards: the substring classifier does match the address.
	require.Contains(t, outage.Error(), "401")
	require.True(t, c.isOAuthError(outage), "precondition: the bare '401' substring matches the port")

	err := c.runAuthStrategies(context.Background(), []authStrategy{
		{"headers", func(context.Context) error { return errors.New("no headers configured") }},
		{"no-auth", func(context.Context) error { return outage }},
		{"OAuth", func(context.Context) error { t.Fatal("a network error must not reach the OAuth strategy"); return nil }},
	}, "")
	require.Error(t, err)
	var dnsErr *net.DNSError
	assert.True(t, errors.As(err, &dnsErr), "the network error is returned as-is")
	assert.Equal(t, "", c.AuthStrategy())
}

func TestRunAuthStrategies_NetworkErrorWith403PortDoesNotReachOAuth(t *testing.T) {
	c := newStrategyTestClient()
	refused := fmt.Errorf("MCP initialize failed during no-auth strategy: %w",
		uptransport.NewError(fmt.Errorf("failed to send request: %w",
			&net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40312},
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}})))
	require.True(t, c.isAuthError(refused), "precondition: the bare '403' substring matches the port")

	err := c.runAuthStrategies(context.Background(), []authStrategy{
		{"no-auth", func(context.Context) error { return refused }},
		{"OAuth", func(context.Context) error { t.Fatal("a network error must not reach the OAuth strategy"); return nil }},
	}, "")
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
}

func TestRunAuthStrategies_RecordsAuthEvidenceForOAuth(t *testing.T) {
	cases := []struct {
		name           string
		anonErr        error
		wantChallenged bool
	}{
		{"typed HTTP 401", fmt.Errorf("MCP initialize failed during no-auth strategy: %w", &uptransport.AuthorizationRequiredError{}), true},
		{"auth-sounding text only", errors.New("MCP initialize failed during no-auth strategy: JSON-RPC error: unauthorized"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newStrategyTestClient()
			var seen *authEvidence
			err := c.runAuthStrategies(context.Background(), []authStrategy{
				{"no-auth", func(context.Context) error { return tc.anonErr }},
				{"OAuth", func(ctx context.Context) error { seen = authEvidenceFrom(ctx); return nil }},
			}, "")
			require.NoError(t, err)
			require.NotNil(t, seen, "the OAuth strategy must receive the ladder's evidence")
			assert.Equal(t, tc.wantChallenged, seen.challenged)
		})
	}
}

func TestSpeculativeOAuthPark(t *testing.T) {
	plain := &Client{config: &config.ServerConfig{Name: "s", URL: "https://u/mcp"}, logger: zap.NewNop()}
	declared := &Client{config: &config.ServerConfig{Name: "s", URL: "https://u/mcp", OAuth: &config.OAuthConfig{}}, logger: zap.NewNop()}

	none := withAuthEvidence(context.Background(), &authEvidence{})
	challenged := withAuthEvidence(context.Background(), &authEvidence{challenged: true})

	assert.True(t, plain.speculativeOAuthPark(none, false), "no oauth block, no token, no 401: a guess")
	assert.False(t, plain.speculativeOAuthPark(challenged, false), "a real 401 confirms the requirement")
	assert.False(t, plain.speculativeOAuthPark(none, true), "a stored token means OAuth was used before")
	assert.False(t, declared.speculativeOAuthPark(none, false), "an oauth block declares the requirement")
	assert.False(t, plain.speculativeOAuthPark(context.Background(), false), "outside the ladder: historical park")
}

// End to end through the real transport: a refused dial to a port whose
// number contains "401" must come back from connectHTTP as a network error,
// never as a deferred OAuth sign-in.
func TestConnectHTTP_RefusedPortWith401IsNotOAuthPending(t *testing.T) {
	var ln net.Listener
	for p := 14010; p < 14020; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			ln = l
			break
		}
	}
	if ln == nil {
		t.Skip("no free port in 14010-14019")
	}
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	require.True(t, strings.Contains(addr, "401"))

	c := &Client{
		config: &config.ServerConfig{Name: "nonauth", URL: "http://" + addr + "/mcp", Protocol: "streamable-http"},
		logger: zap.NewNop(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := c.connectHTTP(ctx)
	require.Error(t, err)
	assert.False(t, IsOAuthPending(err), "a refused dial is not a sign-in requirement: %v", err)
	assert.True(t, IsNetworkError(err), "the network cause survives the ladder: %v", err)
}

// A real 401 still routes to OAuth and is recorded as a confirmed challenge.
func TestRunAuthStrategies_Real401MarksChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &Client{
		config: &config.ServerConfig{Name: "needs-oauth", URL: srv.URL + "/mcp", Protocol: "streamable-http"},
		logger: zap.NewNop(),
	}
	var seen *authEvidence
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := c.runAuthStrategies(ctx, []authStrategy{
		{"no-auth", c.tryNoAuth},
		{"OAuth", func(ctx context.Context) error { seen = authEvidenceFrom(ctx); return errors.New("stop") }},
	}, "")
	require.Error(t, err)
	require.NotNil(t, seen, "a real 401 must still reach the OAuth strategy")
	assert.True(t, seen.challenged)
}
