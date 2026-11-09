package scanner

import (
	"context"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// GH #1722 cause 2: a scan of a server that cannot connect until a person
// completes its OAuth login (a flow in flight, or parked in Pending Auth) used
// to call EnsureConnected, which RESTARTS the server and retires the instance
// that owns the pending flow (PKCE verifier, callback waiter). The scan must
// not reconnect such a server; it scans what it can export without a restart.

type loginGuardProvider struct {
	awaiting       atomic.Bool
	ensureCalls    atomic.Int32
	connectedAfter bool
}

func (p *loginGuardProvider) GetServerInfo(string) (*ServerInfo, error) {
	return &ServerInfo{Name: "oauth-srv", Protocol: "http", URL: "https://example.test/mcp"}, nil
}

func (p *loginGuardProvider) GetServerTools(string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"name": "list_teams", "description": "List teams."}}, nil
}

func (p *loginGuardProvider) EnsureConnected(context.Context, string) error {
	p.ensureCalls.Add(1)
	return nil
}

func (p *loginGuardProvider) IsConnected(string) bool { return p.connectedAfter }

func (p *loginGuardProvider) AwaitingOAuthLogin(string) bool { return p.awaiting.Load() }

func newLoginGuardService(t *testing.T, provider ServerInfoProvider) *Service {
	t.Helper()
	dir := t.TempDir()
	logger := zap.NewNop()
	store := newMockStorage()
	docker := NewDockerRunner(logger)
	registry := NewRegistry(dir, logger)
	svc := NewService(store, registry, docker, dir, logger)
	svc.SetServerInfoProvider(provider)
	return svc
}

func TestStartScan_DoesNotReconnectServerAwaitingOAuthLogin(t *testing.T) {
	provider := &loginGuardProvider{}
	provider.awaiting.Store(true)
	svc := newLoginGuardService(t, provider)

	if _, err := svc.StartScan(context.Background(), "oauth-srv", false, nil, ""); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitForScanIdle(t, svc, "oauth-srv")

	if got := provider.ensureCalls.Load(); got != 0 {
		t.Fatalf("a scan must not restart a server awaiting an OAuth login (would retire the flow); EnsureConnected called %d time(s)", got)
	}
}

// Control: the same disconnected server with no login pending is reconnected
// for the export, as before.
func TestStartScan_ReconnectsDisconnectedServerWithoutLogin(t *testing.T) {
	provider := &loginGuardProvider{}
	svc := newLoginGuardService(t, provider)

	if _, err := svc.StartScan(context.Background(), "oauth-srv", false, nil, ""); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitForScanIdle(t, svc, "oauth-srv")

	if got := provider.ensureCalls.Load(); got == 0 {
		t.Fatal("a disconnected server with no login pending is still reconnected for the scan export")
	}
}
