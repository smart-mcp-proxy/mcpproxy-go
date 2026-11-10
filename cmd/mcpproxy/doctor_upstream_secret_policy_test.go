package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDoctorOutput_UpstreamSecretPolicyDenialPublicOnly(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "allowed")
	outside := filepath.Join(dir, "outside", "deep", "token.pem")
	require.NoError(t, os.MkdirAll(allowed, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o700))
	require.NoError(t, os.WriteFile(outside, []byte("x\n"), 0o600))
	w := config.SecretFileDirs{allowed}
	cfg := &config.Config{DataDir: dir, SecretFileDirs: &w}
	r := config.BuildSecretResolver(cfg, "", secret.ScopeUpstream)
	srvCfg := &config.ServerConfig{
		Name: "denied", Command: "true",
		Env: map[string]string{"TOKEN": "${file:" + outside + "}"},
	}
	_, err := core.NewClientWithOptions("denied", srvCfg, zap.NewNop(), nil, cfg, nil, false, r)
	require.Error(t, err)
	var pe *core.UpstreamSecretPolicyError
	require.ErrorAs(t, err, &pe)

	h := health.CalculateHealth(health.HealthCalculatorInput{
		Name: "denied", Enabled: true, State: "error", Connected: false, LastError: err.Error(),
	}, nil)
	diag := map[string]interface{}{
		"total_issues": 1,
		"upstream_errors": []interface{}{
			map[string]interface{}{
				"server_name":   "denied",
				"error_message": h.Detail,
			},
		},
	}

	oldFmt := doctorOutput
	t.Cleanup(func() { doctorOutput = oldFmt })
	doctorOutput = "pretty"
	var runErr error
	out, _ := captureStd(func() { runErr = outputDiagnostics(diag, nil, nil, "", nil) })
	require.NoError(t, runErr)
	assert.Contains(t, out, pe.Public)
	for _, leak := range []string{outside, "${file:", "TOKEN"} {
		assert.NotContains(t, out, leak)
	}
	assert.True(t, strings.Contains(out, "Upstream Server Connection Errors"))
}
