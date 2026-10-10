package upstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

func newRetireTestManager(t *testing.T) (*Manager, *config.ServerConfig) {
	t.Helper()
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.ShutdownAll(t.Context()) })
	cfg := &config.ServerConfig{Name: "q", URL: "http://127.0.0.1:1/old", Protocol: "http", Enabled: true, Quarantined: true}
	require.NoError(t, m.AddServerConfig("q", cfg))
	return m, cfg
}

// UX-01 r9: once a commit retired a client, a review capture bound to it is no
// longer current, even though the manager still holds the pointer until
// reconciliation replaces it.
func TestWithCurrentClientOnEpoch_RefusesRetiredClient(t *testing.T) {
	m, cfg := newRetireTestManager(t)
	client, ok := m.GetClient("q")
	require.True(t, ok)
	epoch := client.ConnectionEpoch()

	ran := false
	current, err := m.WithCurrentClientOnEpoch("q", client, epoch, func() error { ran = true; return nil })
	require.NoError(t, err)
	require.True(t, current)
	require.True(t, ran)

	replaced := *cfg
	replaced.URL = "http://127.0.0.1:2/new"
	m.RetireStaleClients([]*config.ServerConfig{&replaced})
	require.True(t, client.IsRetired())

	ran = false
	current, err = m.WithCurrentClientOnEpoch("q", client, epoch, func() error { ran = true; return nil })
	require.NoError(t, err)
	require.False(t, current, "a retired client's capture must not be current")
	require.False(t, ran, "persistence ran for a retired client")
}

// A capture already inside its persistence section finishes before the
// retirement returns, so nothing persists after the commit that retired it.
func TestRetireStaleClients_WaitsForCaptureInsidePersistence(t *testing.T) {
	m, cfg := newRetireTestManager(t)
	client, _ := m.GetClient("q")
	epoch := client.ConnectionEpoch()

	inside := make(chan struct{})
	release := make(chan struct{})
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		_, _ = m.WithCurrentClientOnEpoch("q", client, epoch, func() error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside

	replaced := *cfg
	replaced.URL = "http://127.0.0.1:2/new"
	retired := make(chan struct{})
	go func() { m.RetireStaleClients([]*config.ServerConfig{&replaced}); close(retired) }()
	select {
	case <-retired:
		t.Fatal("retirement returned while a capture was still persisting")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	<-captureDone
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("retirement never completed")
	}
}
