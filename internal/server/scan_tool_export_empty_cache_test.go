package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// markConnectedWithoutTools puts serverName in the StateView as connected
// with an empty tool cache (e.g. right after a reconnect, before discovery).
func markConnectedWithoutTools(s *Server, serverName string) {
	s.runtime.Supervisor().StateView().UpdateServer(serverName, func(st *stateview.ServerStatus) {
		st.Connected = true
		st.Tools = nil
	})
}

// TestConfigServerInfoProvider_EmptyCacheLiveFailure_PropagatesError (Sol r8
// P2): a connected StateView entry with zero tools plus a failed live
// tools/list is not an empty tool list — there is no usable definition. The
// provider must report the live failure instead of returning (empty, nil),
// which defeated the scanner's fail-closed handling.
func TestConfigServerInfoProvider_EmptyCacheLiveFailure_PropagatesError(t *testing.T) {
	s := newAdmissionTestServer(t, newFakeSecurityScanner(), &config.ServerConfig{Name: "srv", Enabled: true})
	markConnectedWithoutTools(s, "srv")
	cached, err := s.serverToolMaps("srv", true)
	require.NoError(t, err, "precondition: the StateView reports the server connected")
	require.Empty(t, cached, "precondition: with no cached tools")

	lister := &fakeLiveToolLister{connected: true, err: errors.New("tools/list: transport closed")}
	p := &configServerInfoProvider{cfg: s.runtime.Config(), liveConfig: s.runtime.Config, server: s, liveTools: listerProviderHook("srv", lister)}
	tools, err := p.GetServerTools("srv")
	require.Error(t, err, "a failed live export over an empty cache must not read as zero tools")
	assert.Contains(t, err.Error(), "transport closed")
	assert.Empty(t, tools)

	// A live list that genuinely answers zero tools stays a valid empty list.
	empty := &fakeLiveToolLister{connected: true}
	p.liveTools = listerProviderHook("srv", empty)
	tools, err = p.GetServerTools("srv")
	require.NoError(t, err)
	assert.Empty(t, tools)
}

// TestScanRealProvider_EmptyCacheLiveFailure_FailsClosed drives the REAL
// scanner.Service over the REAL provider: a manual scan of a server with
// source files, whose StateView is connected but empty and whose live
// tools/list fails, must refuse to start rather than scan the source with an
// empty tool snapshot (a baseline with no tool findings).
func TestScanRealProvider_EmptyCacheLiveFailure_FailsClosed(t *testing.T) {
	lister := &fakeLiveToolLister{connected: true, err: errors.New("tools/list: transport closed")}
	s, svc := newScanIntegrationServer(t, lister, &config.ServerConfig{Name: "srv", Enabled: true})
	markConnectedWithoutTools(s, "srv")

	source := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(source, "server.js"), []byte("// source"), 0o644))
	job, err := svc.StartScan(context.Background(), "srv", false, nil, source)
	require.Error(t, err, "the scan must fail closed")
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "transport closed")
	assert.Positive(t, lister.callCount(), "the live export was attempted")
}
