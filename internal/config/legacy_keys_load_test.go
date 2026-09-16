//go:build server

package config_test

// Spec 107 T009 (US5, FR-035/FR-039): a config written against the pre-107
// server edition — carrying server_edition.max_user_servers,
// workspace_idle_timeout, store_idp_tokens: true and per-server auth_broker
// blocks using the never-implemented token_exchange mode or the removed
// header key — must still BOOT. The removed keys/modes are dropped by the
// server-build normaliser on the raw map before the typed decode and recorded
// as exactly one LoadDiagnostic each on the returned Config. The loader has no
// logger, so the diagnostics are emitted later by config.LogLoadDiagnostics,
// which this test drives through a zap observer.
//
// This file is an external test package (config_test) because it also asserts
// runtime.DetectConfigChanges is blind to the dropped keys, and internal/runtime
// imports internal/config.
//
// Sibling: internal/httpapi/config_patch_removed_keys_test.go covers the two
// write doors (PATCH /api/v1/config and /config/apply); this file covers the
// boot door and the raw-document check itself.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

const legacyServerEditionFixture = "testdata/legacy_server_edition.json"

// Exact strings from specs/107-server-edition-sso-hardening/contracts/config-keys.md.
const (
	msgMaxUserServersIgnored       = "server_edition.max_user_servers is no longer supported and was ignored"
	msgWorkspaceIdleTimeoutIgnored = "server_edition.workspace_idle_timeout is no longer supported and was ignored"
	msgStoreIDPTokensDeprecated    = "server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it"
	msgTokenExchangeIgnored        = `auth_broker.mode "token_exchange" was never implemented; the auth_broker block for server "legacy-exchange" was ignored`
	msgAuthBrokerHeaderIgnored     = "auth_broker.header is no longer supported and was ignored"
)

// removedKeyMarkers are the JSON key/value tokens that must never survive a
// write-back of the legacy fixture. store_idp_tokens is NOT in this list: it is
// a retained (deprecated, no-op) decoder field, not a removed key.
var removedKeyMarkers = []string{
	`"max_user_servers"`,
	`"workspace_idle_timeout"`,
	`"token_exchange"`,
	`"header"`,
	`"header_format"`,
}

// readLegacyFixture returns the fixture as a generic document.
func readLegacyFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(legacyServerEditionFixture)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

// writeDocument persists a generic document under dir with data_dir pinned to
// dir (LoadFromFile creates the data directory) and returns its path.
func writeDocument(t *testing.T, dir, name string, doc map[string]any) string {
	t.Helper()
	doc["data_dir"] = dir
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

// cleanSibling strips every removed key/mode from the legacy document the way
// the normaliser is specified to: the whole auth_broker block of a
// token_exchange/entra_obo server goes, only the header/header_format leaves
// go from an oauth_connect server, and the two server_edition keys go.
func cleanSibling(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	var clean map[string]any
	require.NoError(t, json.Unmarshal(raw, &clean))

	se, ok := clean["server_edition"].(map[string]any)
	require.True(t, ok, "fixture must carry a server_edition object")
	delete(se, "max_user_servers")
	delete(se, "workspace_idle_timeout")

	servers, ok := clean["mcpServers"].([]any)
	require.True(t, ok, "fixture must carry mcpServers")
	for _, s := range servers {
		server, ok := s.(map[string]any)
		require.True(t, ok)
		broker, ok := server["auth_broker"].(map[string]any)
		if !ok {
			continue
		}
		switch broker["mode"] {
		case "token_exchange", "entra_obo":
			delete(server, "auth_broker")
		default:
			delete(broker, "header")
			delete(broker, "header_format")
		}
	}
	return clean
}

func loadLegacyFixture(t *testing.T) (*config.Config, map[string]any) {
	t.Helper()
	doc := readLegacyFixture(t)
	path := writeDocument(t, t.TempDir(), "legacy.json", doc)
	cfg, err := config.LoadFromFile(path)
	require.NoError(t, err, "a pre-107 config must still boot the server edition")
	require.NotNil(t, cfg)
	return cfg, doc
}

// Boot door: the legacy fixture loads, the block survives, and exactly one
// LoadDiagnostic is recorded per removed key/mode plus one for the deprecated
// store_idp_tokens: true.
func TestLegacyKeys_BootLoadRecordsOneDiagnosticPerKey(t *testing.T) {
	cfg, _ := loadLegacyFixture(t)

	require.NotNil(t, cfg.ServerEdition, "the server_edition block must survive normalisation")
	assert.True(t, cfg.ServerEdition.Enabled, "dropping removed keys must not disable the block")
	assert.Equal(t, []string{"admin@example.com"}, cfg.ServerEdition.AdminEmails)
	assert.True(t, cfg.ServerEdition.StoreIDPTokens,
		"store_idp_tokens keeps its decoder field (deprecated no-op, FR-035 exemption)")

	require.Len(t, cfg.Servers, 2)
	byName := map[string]*config.ServerConfig{}
	for _, s := range cfg.Servers {
		byName[s.Name] = s
	}
	require.Contains(t, byName, "legacy-exchange")
	require.Contains(t, byName, "legacy-header")
	assert.Nil(t, byName["legacy-exchange"].AuthBroker,
		"a token_exchange block was never implemented: the WHOLE auth_broker block is dropped")
	require.NotNil(t, byName["legacy-header"].AuthBroker,
		"an oauth_connect block loses only its header leaf, not the block")
	assert.Equal(t, "oauth_connect", byName["legacy-header"].AuthBroker.Mode)

	diags := cfg.LoadDiagnostics()
	require.Len(t, diags, 5, "one diagnostic per removed key/mode + one for store_idp_tokens: %+v", diags)

	messages := make([]string, 0, len(diags))
	keys := make([]string, 0, len(diags))
	for _, d := range diags {
		messages = append(messages, d.Message)
		keys = append(keys, d.Key)
		assert.NotEmpty(t, d.Key, "every diagnostic names the key it dropped: %+v", d)
	}
	assert.ElementsMatch(t, []string{
		msgMaxUserServersIgnored,
		msgWorkspaceIdleTimeoutIgnored,
		msgStoreIDPTokensDeprecated,
		msgTokenExchangeIgnored,
		msgAuthBrokerHeaderIgnored,
	}, messages, "messages must match contracts/config-keys.md byte for byte")

	// The three server_edition diagnostics name their key exactly; the two
	// per-server ones end in the leaf they dropped (the contract fixes the
	// message, not the per-server key path).
	assert.Contains(t, keys, "server_edition.max_user_servers")
	assert.Contains(t, keys, "server_edition.workspace_idle_timeout")
	assert.Contains(t, keys, "server_edition.store_idp_tokens")
	var sawMode, sawHeader bool
	for _, k := range keys {
		if strings.HasSuffix(k, "auth_broker.mode") {
			sawMode = true
		}
		if strings.HasSuffix(k, "auth_broker.header") {
			sawHeader = true
		}
	}
	assert.True(t, sawMode, "the token_exchange diagnostic keys on auth_broker.mode: %v", keys)
	assert.True(t, sawHeader, "the header diagnostic keys on auth_broker.header: %v", keys)
}

// A config with none of the removed keys records no diagnostics at all —
// the slice is not a catch-all for every load.
func TestLegacyKeys_CleanConfigRecordsNoDiagnostics(t *testing.T) {
	doc := readLegacyFixture(t)
	clean := cleanSibling(t, doc)
	delete(clean["server_edition"].(map[string]any), "store_idp_tokens")
	path := writeDocument(t, t.TempDir(), "clean.json", clean)

	cfg, err := config.LoadFromFile(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.LoadDiagnostics())
}

// Emit helper: the loader has no logger (loadConfigFile returns only an error
// and main.go builds the logger after the load), so the recorded diagnostics
// are emitted once by config.LogLoadDiagnostics — one WARN line each.
func TestLegacyKeys_LogLoadDiagnosticsEmitsOneWarnPerDiagnostic(t *testing.T) {
	cfg, _ := loadLegacyFixture(t)
	diags := cfg.LoadDiagnostics()
	require.NotEmpty(t, diags)

	core, logs := observer.New(zapcore.DebugLevel)
	config.LogLoadDiagnostics(cfg, zap.New(core))

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	require.Len(t, warns, len(diags), "exactly one WARN line per diagnostic; got: %+v", logs.All())
	assert.Equal(t, len(diags), logs.Len(), "nothing but the WARN lines is emitted")

	joined := make([]string, 0, len(warns))
	for _, entry := range warns {
		line := entry.Message
		for _, f := range entry.Context {
			line += " " + f.Key + "=" + fieldString(f)
		}
		joined = append(joined, line)
	}
	for _, d := range diags {
		found := false
		for _, line := range joined {
			if strings.Contains(line, d.Message) {
				found = true
				break
			}
		}
		assert.True(t, found, "diagnostic %q must appear in one WARN line: %v", d.Message, joined)
	}

	// Emitting from an empty config is a no-op, not a panic and not a line.
	core2, logs2 := observer.New(zapcore.DebugLevel)
	config.LogLoadDiagnostics(config.DefaultConfig(), zap.New(core2))
	assert.Zero(t, logs2.Len())
}

// fieldString renders a zap field's value the way a test can grep it.
func fieldString(f zapcore.Field) string {
	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)
	if v, ok := enc.Fields[f.Key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

// Hot-reload reporting: the dropped keys are never compared, so a legacy file
// and its clean sibling are the SAME config to DetectConfigChanges.
func TestLegacyKeys_DetectConfigChangesIsBlindToDroppedKeys(t *testing.T) {
	doc := readLegacyFixture(t)
	dir := t.TempDir()
	legacyPath := writeDocument(t, dir, "legacy.json", doc)
	cleanPath := writeDocument(t, dir, "clean.json", cleanSibling(t, doc))

	legacyCfg, err := config.LoadFromFile(legacyPath)
	require.NoError(t, err)
	cleanCfg, err := config.LoadFromFile(cleanPath)
	require.NoError(t, err)

	for name, pair := range map[string][2]*config.Config{
		"clean→legacy": {cleanCfg, legacyCfg},
		"legacy→clean": {legacyCfg, cleanCfg},
	} {
		result := runtime.DetectConfigChanges(pair[0], pair[1])
		require.NotNil(t, result, name)
		assert.True(t, result.Success, name)
		assert.False(t, result.RequiresRestart, "%s: %+v", name, result)
		assert.Empty(t, result.ChangedFields, "%s: dropped keys must not surface as a change", name)
		for _, f := range result.ChangedFields {
			for _, marker := range []string{"max_user_servers", "workspace_idle_timeout", "auth_broker", "store_idp_tokens"} {
				assert.NotContains(t, f, marker, name)
			}
		}
	}
}

// Write-back: SaveConfig of the loaded legacy config omits every removed
// key/mode — the file is rewritten clean, so the diagnostics fire once, not on
// every subsequent boot.
func TestLegacyKeys_WriteBackOmitsRemovedKeys(t *testing.T) {
	cfg, _ := loadLegacyFixture(t)

	out := filepath.Join(t.TempDir(), "written.json")
	require.NoError(t, config.SaveConfig(cfg, out))
	written, err := os.ReadFile(out)
	require.NoError(t, err)
	text := string(written)
	for _, marker := range removedKeyMarkers {
		assert.NotContains(t, text, marker, "write-back must not resurrect a removed key/mode")
	}

	// And the rewritten file loads with no diagnostics except the retained
	// store_idp_tokens deprecation (its decoder field survives by design).
	reloaded, err := config.LoadFromFile(out)
	require.NoError(t, err)
	for _, d := range reloaded.LoadDiagnostics() {
		assert.Equal(t, msgStoreIDPTokensDeprecated, d.Message,
			"only the deprecated store_idp_tokens may still be reported after a clean write-back")
	}
}

// Raw-document check: the refusal the two HTTP write doors use. It walks the
// generic map, so it sees keys json.Unmarshal into Config silently drops.
// store_idp_tokens is deprecated, not removed, and is NOT refused here.
func TestLegacyKeys_ValidateRemovedKeysWalksTheRawDocument(t *testing.T) {
	doc := readLegacyFixture(t)

	errs := config.ValidateRemovedKeys(doc)
	require.Len(t, errs, 4, "max_user_servers, workspace_idle_timeout, token_exchange mode, header: %+v", errs)

	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		messages = append(messages, e.Message)
		assert.NotEmpty(t, e.Field, "%+v", e)
		assert.NotContains(t, e.Message, "store_idp_tokens", "deprecated is not removed")
	}
	assert.ElementsMatch(t, []string{
		msgMaxUserServersIgnored,
		msgWorkspaceIdleTimeoutIgnored,
		msgTokenExchangeIgnored,
		msgAuthBrokerHeaderIgnored,
	}, messages)

	fields := make([]string, 0, len(errs))
	for _, e := range errs {
		fields = append(fields, e.Field)
	}
	assert.Contains(t, fields, "server_edition.max_user_servers")
	assert.Contains(t, fields, "server_edition.workspace_idle_timeout")

	// The clean sibling passes; a nil/empty document passes.
	assert.Empty(t, config.ValidateRemovedKeys(cleanSibling(t, doc)))
	assert.Empty(t, config.ValidateRemovedKeys(nil))
	assert.Empty(t, config.ValidateRemovedKeys(map[string]any{}))

	// entra_obo and header_format are refused the same way.
	obo := map[string]any{
		"mcpServers": []any{map[string]any{
			"name":        "obo",
			"url":         "https://obo.example.com/mcp",
			"protocol":    "http",
			"auth_broker": map[string]any{"mode": "entra_obo", "header_format": "Token {token}"},
		}},
	}
	oboErrs := config.ValidateRemovedKeys(obo)
	require.Len(t, oboErrs, 2, "%+v", oboErrs)
	var sawOBO, sawHeaderFormat bool
	for _, e := range oboErrs {
		if e.Message == `auth_broker.mode "entra_obo" was never implemented; the auth_broker block for server "obo" was ignored` {
			sawOBO = true
		}
		if e.Message == "auth_broker.header_format is no longer supported and was ignored" {
			sawHeaderFormat = true
		}
	}
	assert.True(t, sawOBO, "%+v", oboErrs)
	assert.True(t, sawHeaderFormat, "%+v", oboErrs)
}

// Config.Validate alone cannot see the removed keys: a bare json.Unmarshal into
// the typed struct drops them without a trace (no field, no diagnostic), which
// is exactly why the write doors need the raw-document check above rather
// than a Validate rule.
func TestLegacyKeys_TypedDecodeDropsThemWithoutTrace(t *testing.T) {
	data, err := os.ReadFile(legacyServerEditionFixture)
	require.NoError(t, err)

	var typed config.Config
	require.NoError(t, json.Unmarshal(data, &typed))
	assert.Empty(t, typed.LoadDiagnostics(),
		"a bare Unmarshal is not the loader: it records nothing")

	back, err := json.Marshal(&typed)
	require.NoError(t, err)
	for _, marker := range []string{`"max_user_servers"`, `"workspace_idle_timeout"`, `"header"`, `"header_format"`} {
		assert.NotContains(t, string(back), marker,
			"the typed struct has no field for a removed key, so nothing downstream of the decode can refuse it")
	}
}
