//go:build server

package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 T052: the doctor seam. config.DoctorFindings over the LIVE config
// is a runtime-warning source; Doctor appends its findings to RuntimeWarnings
// and counts them in TotalIssues, re-reading the config on every call.
func TestDoctor_ServerEditionFindingsThroughRuntimeWarningSource(t *testing.T) {
	block := func(publicURL, secure string) *config.ServerEditionConfig {
		return &config.ServerEditionConfig{
			Enabled:             true,
			AdminEmails:         []string{"admin@example.com"},
			OAuth:               &config.ServerEditionOAuthConfig{Provider: "google", ClientID: "id", ClientSecret: "secret"},
			PublicURL:           publicURL,
			SessionCookieSecure: secure,
		}
	}
	live := &config.Config{Listen: "0.0.0.0:8080", RequireMCPAuth: false, ServerEdition: block("", "false")}

	svc := NewService(newMockRuntime(), live, "", &mockEventEmitter{}, nil, zap.NewNop().Sugar())
	svc.AddRuntimeWarningSource(func() []string { return config.DoctorFindings(live) })

	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 3, "%v", diag.RuntimeWarnings)
	assert.Contains(t, diag.RuntimeWarnings[0], config.MsgRequireMCPAuthOverridden)
	assert.Contains(t, diag.RuntimeWarnings[1], "server_edition.public_url is unset")
	assert.Contains(t, diag.RuntimeWarnings[1], "0.0.0.0:8080")
	assert.Contains(t, diag.RuntimeWarnings[2], "session_cookie_secure")
	assert.Equal(t, 3, diag.TotalIssues, "runtime warnings count as issues")

	// Live: the next call reads the current config.
	live.RequireMCPAuth = true
	live.ServerEdition = block("https://mcp.example.com", "auto")
	diag, err = svc.Doctor(context.Background())
	require.NoError(t, err)
	assert.Empty(t, diag.RuntimeWarnings)
	assert.Equal(t, 0, diag.TotalIssues)

	// Loopback listener without public_url is not a finding.
	live.RequireMCPAuth = false
	live.Listen = "127.0.0.1:8080"
	live.ServerEdition = block("", "")
	diag, err = svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.RuntimeWarnings, 1)
	assert.Contains(t, diag.RuntimeWarnings[0], config.MsgRequireMCPAuthOverridden)
}

func TestDoctorFindings_PersonalShapeAndDisabledBlock(t *testing.T) {
	assert.Empty(t, config.DoctorFindings(nil))
	assert.Empty(t, config.DoctorFindings(&config.Config{Listen: "0.0.0.0:8080"}), "no block, no finding")
	assert.Empty(t, config.DoctorFindings(&config.Config{Listen: "0.0.0.0:8080", ServerEdition: &config.ServerEditionConfig{Enabled: false, SessionCookieSecure: "false"}}), "disabled block, no finding")
}
