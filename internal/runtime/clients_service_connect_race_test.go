package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// hookMinter runs afterIssue between the real Issue (staged secret B) and the
// config write, the window the FR-021a race lives in.
type hookMinter struct {
	connect.CredentialMinter
	afterIssue func()
}

func (m hookMinter) Issue(clientID string, intent connect.CredentialIntent) (*connect.IssuedCredential, error) {
	issued, err := m.CredentialMinter.Issue(clientID, intent)
	if err == nil && m.afterIssue != nil {
		m.afterIssue()
	}
	return issued, err
}

// TestConnect_ReconcileRaceThroughConnectService drives a real connect.Service:
// a reconcile that fires after the credential is staged but before the config
// file is rewritten must not roll the new secret back, so the secret the file
// ends up holding still authenticates.
func TestConnect_ReconcileRaceThroughConnectService(t *testing.T) {
	h := newSvcHarness(t)
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", home+"/AppData/Local")
	t.Setenv("APPDATA", home+"/AppData/Roaming")
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "admin-key", home).WithRequireMCPAuth(true)
	svc.WithCredentialMinter(h.svc.ConnectMinter())
	h.svc.SetConfigReader(svc)

	cfgPath := connect.ConfigPath("cursor", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte("{}\n"), 0o644))

	prof := "ro"
	res, err := svc.ConnectWithOptions("cursor", "", connect.ConnectOptions{Intent: connect.CredentialIntent{Profile: &prof, ActorKind: "api_key", Surface: "api"}})
	require.NoError(t, err)
	require.True(t, res.Success)
	first, found, err := svc.ClientSecret("cursor")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, h.authenticates(first))

	svc.WithCredentialMinter(hookMinter{
		CredentialMinter: h.svc.ConnectMinter(),
		afterIssue: func() {
			require.NoError(t, h.svc.ReconcileClient(context.Background(), "cursor"))
		},
	})
	res, err = svc.ConnectWithOptions("cursor", "", connect.ConnectOptions{Force: true, Intent: connect.CredentialIntent{ActorKind: "api_key", Surface: "api"}})
	require.NoError(t, err)
	require.True(t, res.Success)

	second, found, err := svc.ClientSecret("cursor")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, first, second)
	require.True(t, h.authenticates(second), "the secret the file holds must authenticate")
	require.False(t, h.authenticates(first))
	require.Equal(t, map[string]int{"finalized": 1}, rotateOutcomes(h))
}
