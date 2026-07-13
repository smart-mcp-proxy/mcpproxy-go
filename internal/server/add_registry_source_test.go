package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// MCP-866: `registry add-source` derivation core.

func TestBuildRegistrySourceEntry_DerivesFieldsAndProvenance(t *testing.T) {
	entry, err := buildRegistrySourceEntry("https://registry.acme.example/", "", "", "")
	require.NoError(t, err)

	assert.Equal(t, config.RegistryProvenanceCustom, entry.Provenance, "user-added source is always custom/unverified")
	assert.Equal(t, "modelcontextprotocol/registry", entry.Protocol, "default protocol")
	assert.NotEmpty(t, entry.ID, "id derived from host")
	assert.Equal(t, "https://registry.acme.example/", entry.URL)
	// servers_url points at the v0.1 servers collection for the generic protocol.
	assert.Equal(t, "https://registry.acme.example/v0.1/servers", entry.ServersURL)
}

func TestBuildRegistrySourceEntry_RejectsNonHTTPS(t *testing.T) {
	for _, bad := range []string{"http://acme.example/", "ftp://acme.example", "not a url", "", "https://"} {
		_, err := buildRegistrySourceEntry(bad, "", "", "")
		require.Errorf(t, err, "must reject %q", bad)
		assert.Truef(t, errors.Is(err, ErrInvalidRegistryURL), "want ErrInvalidRegistryURL for %q, got %v", bad, err)
	}
}

// TestBuildRegistrySourceEntry_RejectsSSRFLiteralIP is the add-source SSRF
// fail-fast (MCP-1076 / CWE-918): an https source whose host is a literal IP in
// a blocked range (loopback, private, link-local/metadata) is refused up front
// with the stable invalid_registry_url code. Public hostnames are unaffected.
func TestBuildRegistrySourceEntry_RejectsSSRFLiteralIP(t *testing.T) {
	for _, bad := range []string{
		"https://169.254.169.254/v0.1/servers", // cloud metadata endpoint
		"https://127.0.0.1/v0.1/servers",       // loopback
		"https://10.0.0.5/",                    // RFC1918 private
		"https://192.168.1.1/",                 // RFC1918 private
		"https://[::1]/v0.1/servers",           // IPv6 loopback
	} {
		_, err := buildRegistrySourceEntry(bad, "", "", "")
		require.Errorf(t, err, "must reject SSRF target %q", bad)
		assert.Truef(t, errors.Is(err, ErrInvalidRegistryURL), "want ErrInvalidRegistryURL for %q, got %v", bad, err)
	}
	// A public hostname source is still accepted.
	if _, err := buildRegistrySourceEntry("https://registry.acme.example/", "", "", ""); err != nil {
		t.Errorf("public hostname source rejected: %v", err)
	}
}

func TestBuildRegistrySourceEntry_HonorsExplicitIDNameAndServersURL(t *testing.T) {
	entry, err := buildRegistrySourceEntry("https://acme.example/v0.1/servers", "modelcontextprotocol/registry", "acme", "Acme Corp")
	require.NoError(t, err)
	assert.Equal(t, "acme", entry.ID)
	assert.Equal(t, "Acme Corp", entry.Name)
	// A URL already pointing at a servers collection is used verbatim.
	assert.Equal(t, "https://acme.example/v0.1/servers", entry.ServersURL)
}

// TestDeriveServersURL_LeavesConcreteURLsAlone is GH discussion #783: MCPProxy
// appended its own route to whatever URL the user pasted, so a static registry
// document (Fleur's apps.json) was fetched as ".../apps.json/v0.1/servers" and
// 404'd. Only a bare base URL (no path) may be pointed at the v0.1 collection.
func TestDeriveServersURL_LeavesConcreteURLsAlone(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"bare host gets the v0.1 collection", "https://registry.acme.example", "https://registry.acme.example/v0.1/servers"},
		{"trailing slash gets the v0.1 collection", "https://registry.acme.example/", "https://registry.acme.example/v0.1/servers"},
		{"explicit servers endpoint is verbatim", "https://acme.example/v0.1/servers", "https://acme.example/v0.1/servers"},
		{"static json document is verbatim", "https://raw.githubusercontent.com/fleuristes/app-registry/refs/heads/main/apps.json", "https://raw.githubusercontent.com/fleuristes/app-registry/refs/heads/main/apps.json"},
		{"path-carrying URL is verbatim", "https://lobehub.com/mcp", "https://lobehub.com/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, deriveServersURL(tt.raw))
		})
	}
}

// stubProbe swaps the live registry probe for the duration of a test.
func stubProbe(t *testing.T, fn func(ctx context.Context, rawURL string) (*registries.SourceProbe, error)) {
	t.Helper()
	prev := probeRegistrySource
	probeRegistrySource = fn
	t.Cleanup(func() { probeRegistrySource = prev })
}

// TestResolveRegistrySourceShape_AdoptsProbedURL: the probe — not an assumption
// — decides the stored ServersURL/Protocol (GH #783). The URL the user pasted is
// what gets probed, and what gets stored.
func TestResolveRegistrySourceShape_AdoptsProbedURL(t *testing.T) {
	const base = "https://registry.acme.example"
	stubProbe(t, func(_ context.Context, rawURL string) (*registries.SourceProbe, error) {
		assert.Equal(t, base, rawURL, "the probe must see the URL the user pasted")
		return &registries.SourceProbe{ServersURL: base + "/v0.1/servers", Protocol: "modelcontextprotocol/registry"}, nil
	})

	entry, err := buildRegistrySourceEntry(base, "", "", "")
	require.NoError(t, err)
	srv := &Server{logger: zap.NewNop()}
	require.NoError(t, srv.resolveRegistrySourceShape(&entry))

	assert.Equal(t, base+"/v0.1/servers", entry.ServersURL)
	assert.Equal(t, "modelcontextprotocol/registry", entry.Protocol)
}

// An unsupported protocol is rejected outright. MCPProxy speaks exactly one
// registry protocol; naming another is a mistake worth reporting, not something
// to accept and then fail to parse.
func TestBuildRegistrySourceEntry_RejectsUnsupportedProtocol(t *testing.T) {
	_, err := buildRegistrySourceEntry("https://acme.example/", "custom/json", "", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedRegistryProtocol), "got %v", err)
	assert.Equal(t, "unsupported_registry_protocol", AddRegistrySourceErrorCode(err),
		"a protocol problem must not be reported as a URL problem — the URL was fine")

	// The one supported protocol, named explicitly, is fine.
	_, err = buildRegistrySourceEntry("https://acme.example/", "modelcontextprotocol/registry", "", "")
	assert.NoError(t, err)
}

// A source that answers but is not a server list is REFUSED at add time, with
// the stable registry_source_unusable code — instead of being persisted and
// failing every later search with an opaque 404.
func TestResolveRegistrySourceShape_RefusesUnusableSource(t *testing.T) {
	stubProbe(t, func(_ context.Context, _ string) (*registries.SourceProbe, error) {
		return nil, fmt.Errorf("%w (https://acme.example/docs: registry query returned 404 Not Found)", registries.ErrRegistrySourceUnusable)
	})

	entry, err := buildRegistrySourceEntry("https://acme.example/docs", "", "", "")
	require.NoError(t, err)
	srv := &Server{logger: zap.NewNop()}

	err = srv.resolveRegistrySourceShape(&entry)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistrySourceUnusable))
	assert.Equal(t, "registry_source_unusable", AddRegistrySourceErrorCode(err))
	assert.Contains(t, err.Error(), "404", "the user must see why it was refused")
}

// An unreachable source (offline, DNS failure) is NOT a verdict on the URL: the
// registry is still added, with the offline-derived defaults.
func TestResolveRegistrySourceShape_ToleratesUnreachableSource(t *testing.T) {
	stubProbe(t, func(_ context.Context, _ string) (*registries.SourceProbe, error) {
		return nil, fmt.Errorf("%w: dial tcp: no such host", registries.ErrRegistrySourceUnreachable)
	})

	entry, err := buildRegistrySourceEntry("https://registry.acme.example/", "", "", "")
	require.NoError(t, err)
	srv := &Server{logger: zap.NewNop()}

	require.NoError(t, srv.resolveRegistrySourceShape(&entry), "an offline probe must not block the add")
	assert.Equal(t, "https://registry.acme.example/v0.1/servers", entry.ServersURL)
	assert.Equal(t, "modelcontextprotocol/registry", entry.Protocol)
}

func TestValidateNewRegistrySource_RejectsLocked(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.RegistriesLocked = true
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistriesLocked))
}

func TestValidateNewRegistrySource_RejectsShadowingBuiltin(t *testing.T) {
	cfg := config.DefaultConfig()
	// "official" is a shipped default — a user must not be able to shadow it.
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "official", URL: "https://evil.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRegistryShadowsBuiltin))
}

func TestValidateNewRegistrySource_RejectsDuplicateCustom(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Registries = []config.RegistryEntry{{ID: "acme", URL: "https://acme.example/", Provenance: config.RegistryProvenanceCustom}}
	err := validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDuplicateRegistry))
}

func TestValidateNewRegistrySource_AllowsNewCustom(t *testing.T) {
	cfg := config.DefaultConfig()
	assert.NoError(t, validateNewRegistrySource(cfg, config.RegistryEntry{ID: "acme", URL: "https://acme.example/"}))
}
