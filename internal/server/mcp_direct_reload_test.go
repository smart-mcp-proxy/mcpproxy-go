package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 102 US4 (T067/T069) — the FR-014 reload guard.
//
// Flipping direct_tool_response_mode on a running proxy has to rebuild the
// direct surface, because that listing is REGISTERED state rather than rendered
// per request: unlike retrieve_tools, reading the live config on the next call
// changes nothing. The guard is what keeps that from becoming churn — every
// unrelated config edit would otherwise re-register every tool and push a
// notifications/tools/list_changed to every connected client.

func newReloadGuardProxy(t *testing.T, mode string) *MCPProxyServer {
	t.Helper()
	p := &MCPProxyServer{
		config: &config.Config{
			RoutingMode:            config.RoutingModeDirect,
			DirectToolResponseMode: mode,
		},
		logger: zap.NewNop(),
	}
	p.initRoutingModeServers()
	return p
}

// A flip is drift; an unrelated edit is not.
func TestDirectSerializationDrifted_OnlyOnARealFlip(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	require.False(t, p.directSerializationDrifted(),
		"a freshly built proxy is already serving its configured mode")

	// The operator flips the setting; the live snapshot moves, the catalog has
	// not been rebuilt yet.
	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	assert.True(t, p.directSerializationDrifted(), "a flip must be detected as drift")

	p.RefreshDirectModeTools()
	assert.False(t, p.directSerializationDrifted(),
		"after the rebuild the published mode matches again — a second reload must not churn")

	// An unrelated edit moves nothing.
	p.config.DebugSearch = !p.config.DebugSearch
	assert.False(t, p.directSerializationDrifted(),
		"an unrelated config edit must not trigger a rebuild (FR-014 no churn)")
}

// "" and "full" are the same mode, so normalizing one to the other must not
// read as drift — otherwise the first reload after an operator deletes the key
// rebuilds for nothing.
func TestDirectSerializationDrifted_EmptyAndFullAreTheSameMode(t *testing.T) {
	p := newReloadGuardProxy(t, "")
	require.Equal(t, config.DirectToolResponseModeFull, p.loadDirectCatalog().Mode(),
		"the empty value must be recorded as the mode it means, not as empty")

	p.config.DirectToolResponseMode = config.DirectToolResponseModeFull
	assert.False(t, p.directSerializationDrifted())
}

// T069: a flip made while DiscoverTools is failing must still be recorded. The
// catalog published on that path is empty, but it carries the NEW mode — if it
// did not, the next reload would see no drift and the operator's change would
// be lost until something unrelated happened to rebuild.
func TestDirectSerializationDrifted_FlipSurvivesAFailingDiscovery(t *testing.T) {
	// No upstream manager: buildDirectModeTools takes its failure path.
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	require.Nil(t, p.upstreamManager)

	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	require.True(t, p.directSerializationDrifted())

	p.RefreshDirectModeTools()

	cat := p.loadDirectCatalog()
	require.NotNil(t, cat, "the failure path publishes an EMPTY catalog, never a nil one")
	assert.Zero(t, cat.Len())
	assert.Equal(t, config.DirectToolResponseModeDeferred, cat.Mode(),
		"the mode must be recorded even when there was nothing to render")
	assert.False(t, p.directSerializationDrifted(),
		"the flip is not lost: a later reload sees no drift and does not churn")
}

// The guard reads the LIVE snapshot, never the construction-time config. The
// two are the same object on a bare proxy, so this drives the real accessor.
func TestDirectSerializationDrifted_NilCatalogIsNotDrift(t *testing.T) {
	p := &MCPProxyServer{
		config: &config.Config{DirectToolResponseMode: config.DirectToolResponseModeDeferred},
		logger: zap.NewNop(),
	}
	require.Nil(t, p.loadDirectCatalog())
	assert.False(t, p.directSerializationDrifted(),
		"nothing published means nothing to compare; treating it as drift would rebuild on every unrelated reload")
}

// A rebuild re-registers the tool set, which is what makes mcp-go emit
// notifications/tools/list_changed, and it bumps the generation exactly once.
func TestReloadRebuild_BumpsGenerationExactlyOnce(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	before := p.loadDirectCatalog().Generation()

	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	p.RefreshDirectModeTools()

	after := p.loadDirectCatalog()
	assert.Equal(t, before+1, after.Generation(), "one publish per rebuild")
	assert.Equal(t, config.DirectToolResponseModeDeferred, after.Mode())

	// The built-ins survive the flip: a rebuild replaces the whole registry.
	require.NotNil(t, p.directServer.GetTool("describe_tool"),
		"describe_tool must survive a serialization flip (FR-018)")
}

// The skew tests stage the SetTools-then-publish window by hand, because
// DiscoverTools has no injection seam (research.md R12). That leaves one thing
// they cannot show: that the real publisher does the two operations in that
// order and leaves them agreeing. This drives RefreshDirectModeTools itself and
// checks the postcondition — every registered upstream name is admitted by the
// catalog that was published with it, and vice versa.
func TestRefreshDirectModeTools_LeavesRegistryAndCatalogAgreeing(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeDeferred)
	p.RefreshDirectModeTools()

	cat := p.loadDirectCatalog()
	require.NotNil(t, cat)

	admitted := map[string]struct{}{}
	for _, name := range cat.DisplayNames() {
		admitted[name] = struct{}{}
	}

	for name := range p.directServer.ListTools() {
		if _, _, isUpstream := ParseDirectToolName(name); !isUpstream {
			continue // a built-in: no catalog entry by design
		}
		assert.Containsf(t, admitted, name,
			"registered %q has no entry in the catalog published with it", name)
		delete(admitted, name)
	}
	assert.Emptyf(t, admitted,
		"the catalog admits names the registry is not serving: %v", admitted)
}

// The SetTools-then-publish order is load-bearing (D13 rule 1) and is invisible
// once a rebuild completes — both land consistently whichever way round they
// went, so inverting them fails nothing. This observes the REAL publisher
// mid-flight through the one seam that exists for it.
func TestRefreshDirectModeTools_PublishesTheRegistryBeforeTheCatalog(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	generationBefore := p.loadDirectCatalog().Generation()

	var sawRegistryAheadOfCatalog bool
	p.directRebuildPause = func() {
		// The registry has been replaced; the catalog has not been published
		// yet, so the generation is still the previous one.
		sawRegistryAheadOfCatalog = p.loadDirectCatalog().Generation() == generationBefore
	}

	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	p.RefreshDirectModeTools()

	assert.True(t, sawRegistryAheadOfCatalog,
		"the catalog must not be published until after SetTools has landed — publishing first would expose a catalog entry for a name the registry is not serving")
	assert.Equal(t, generationBefore+1, p.loadDirectCatalog().Generation())
}
