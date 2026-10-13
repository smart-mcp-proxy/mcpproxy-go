//go:build !server

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 FR-040 (T010, personal build, PATCH leg): load the shared fixture,
// save it, drive an UNRELATED key through the handlePatchConfig merge path
// (marshal base → UseNumber map → MergeConfigPatch → marshal → typed decode),
// save again, and assert the `server_edition` block and every `auth_broker`
// block are still semantically identical to the originals — numbers by
// decimal text, with 9007199254740993 and 0.1000000000000000055511151231257827
// planted in every block.
//
// Compile-red until T020 exports MergeConfigPatch (a one-line wrapper over
// deepMergeJSON, server.go) and lands the json.RawMessage carriers; the
// UseNumber decode below mirrors what T020 makes handlePatchConfig do.

const (
	patchRoundTripFixture = "../config/testdata/legacy_server_edition.json"
	patchProbeIntKey      = "roundtrip_probe_int"
	patchProbeDecKey      = "roundtrip_probe_dec"
	patchProbeInt         = "9007199254740993"
	patchProbeDec         = "0.1000000000000000055511151231257827"
)

func TestConfigPatch_OpaqueBlocksSurviveUnrelatedPatch(t *testing.T) {
	dir := t.TempDir()
	original, planted := plantPatchProbes(t, patchRoundTripFixture, dir)

	src := filepath.Join(dir, "planted.json")
	require.NoError(t, os.WriteFile(src, planted, 0o600))
	firstSave := filepath.Join(dir, "after_first_save.json")
	secondSave := filepath.Join(dir, "after_patch_save.json")

	const patchedListen = "127.0.0.1:19107"
	var stderr bytes.Buffer
	captureLoaderStderr(t, &stderr, func() {
		cfg, err := config.LoadFromFile(src)
		require.NoError(t, err)
		require.NoError(t, config.SaveConfig(cfg, firstSave))

		// handlePatchConfig: base = desired (on-disk) config marshalled and
		// decoded as a generic map; patch = the client body. Both decodes use
		// UseNumber so every number rides through the merge as decimal text.
		base, err := config.LoadFromFile(firstSave)
		require.NoError(t, err)
		baseBytes, err := json.Marshal(base)
		require.NoError(t, err)
		baseMap := decodePatchUseNumber(t, baseBytes)
		patchMap := decodePatchUseNumber(t, []byte(`{"listen":"`+patchedListen+`"}`))

		merged := MergeConfigPatch(baseMap, patchMap)
		mergedBytes, err := json.Marshal(merged)
		require.NoError(t, err)
		var mergedCfg config.Config
		require.NoError(t, json.Unmarshal(mergedBytes, &mergedCfg))
		require.NoError(t, config.SaveConfig(&mergedCfg, secondSave))
	})
	require.NotContains(t, stderr.String(), "WARN", "the personal build must not warn about the opaque blocks")

	saved, err := os.ReadFile(secondSave)
	require.NoError(t, err)
	got := decodePatchUseNumber(t, saved)
	require.Equal(t, patchedListen, got["listen"], "the unrelated patch must have been applied")

	if diff := patchStructuralDiff("server_edition", original["server_edition"], got["server_edition"]); diff != "" {
		t.Errorf("server_edition block damaged by an unrelated PATCH: %s", diff)
	}
	want := patchAuthBrokersByName(original)
	have := patchAuthBrokersByName(got)
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if diff := patchStructuralDiff("mcpServers["+name+"].auth_broker", want[name], have[name]); diff != "" {
			t.Errorf("auth_broker block for server %q damaged by an unrelated PATCH: %s", name, diff)
		}
	}
}

// TestConfigPatch_HandlerPreservesOpaqueBlocks is the same property driven
// through the PRODUCTION door: an HTTP PATCH of an unrelated key reaches
// handlePatchConfig, whose own decoders (patch body and marshalled base) must
// both be UseNumber for the planted 9007199254740993 /
// 0.1000000000000000055511151231257827 to survive into the *config.Config
// handed to ApplyConfig. The test above calls MergeConfigPatch with its own
// UseNumber decode and so cannot see the handler's decoders at all.
//
// BITES: drop either `.UseNumber()` in handlePatchConfig — the base decoder
// turns 9007199254740993 into 9007199254740992 and the decimal into
// 0.1 — and the structural diff names the damaged key.
func TestConfigPatch_HandlerPreservesOpaqueBlocks(t *testing.T) {
	dir := t.TempDir()
	original, planted := plantPatchProbes(t, patchRoundTripFixture, dir)

	src := filepath.Join(dir, "planted.json")
	require.NoError(t, os.WriteFile(src, planted, 0o600))
	live, err := config.LoadFromFile(src)
	require.NoError(t, err)

	// The mock returns the loaded document as the live config and captures
	// exactly what the handler hands to ApplyConfig.
	ctrl := &mockPatchConfigController{apiKey: "test-key", live: live}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	const patchedListen = "127.0.0.1:19108"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", strings.NewReader(`{"listen":"`+patchedListen+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.NotNil(t, ctrl.captured, "ApplyConfig must have been reached")
	require.Equal(t, patchedListen, ctrl.captured.Listen, "the unrelated patch must have been applied")

	// Persist what the handler produced, the way ApplyConfig would, and
	// compare the opaque blocks against the planted originals.
	saved := filepath.Join(dir, "after_handler_patch.json")
	require.NoError(t, config.SaveConfig(ctrl.captured, saved))
	data, err := os.ReadFile(saved)
	require.NoError(t, err)
	got := decodePatchUseNumber(t, data)

	if diff := patchStructuralDiff("server_edition", original["server_edition"], got["server_edition"]); diff != "" {
		t.Errorf("server_edition block damaged by PATCH /api/v1/config: %s", diff)
	}
	want := patchAuthBrokersByName(original)
	have := patchAuthBrokersByName(got)
	require.NotEmpty(t, want, "fixture must carry auth_broker blocks")
	for name, block := range want {
		if diff := patchStructuralDiff("mcpServers["+name+"].auth_broker", block, have[name]); diff != "" {
			t.Errorf("auth_broker block for server %q damaged by PATCH /api/v1/config: %s", name, diff)
		}
	}
}

func plantPatchProbes(t *testing.T, fixture, dataDir string) (map[string]any, []byte) {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	require.NoError(t, err, "shared fixture %s (created by T009) must exist", fixture)

	doc := decodePatchUseNumber(t, raw)
	doc["data_dir"] = filepath.ToSlash(dataDir)

	se, ok := doc["server_edition"].(map[string]any)
	require.True(t, ok, "fixture must carry a server_edition object")
	se[patchProbeIntKey] = json.Number(patchProbeInt)
	se[patchProbeDecKey] = json.Number(patchProbeDec)

	servers, _ := doc["mcpServers"].([]any)
	planted := 0
	for _, s := range servers {
		server, ok := s.(map[string]any)
		if !ok {
			continue
		}
		broker, ok := server["auth_broker"].(map[string]any)
		if !ok {
			continue
		}
		broker[patchProbeIntKey] = json.Number(patchProbeInt)
		broker[patchProbeDecKey] = json.Number(patchProbeDec)
		planted++
	}
	require.Greater(t, planted, 0, "fixture must carry at least one mcpServers[].auth_broker object")

	out, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	return decodePatchUseNumber(t, out), out
}

func patchAuthBrokersByName(doc map[string]any) map[string]any {
	out := map[string]any{}
	servers, _ := doc["mcpServers"].([]any)
	for _, s := range servers {
		server, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := server["name"].(string)
		if broker, present := server["auth_broker"]; present {
			out[name] = broker
		}
	}
	return out
}

func decodePatchUseNumber(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	require.NoError(t, dec.Decode(&doc))
	return doc
}

// patchStructuralDiff is the independent UseNumber comparator (mirrors the
// one in internal/config/personal_roundtrip_test.go; test helpers are not
// importable across packages).
func patchStructuralDiff(path string, want, got any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: want object, got %s", path, patchDescribe(got))
		}
		keys := map[string]struct{}{}
		for k := range w {
			keys[k] = struct{}{}
		}
		for k := range g {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			wv, inWant := w[k]
			gv, inGot := g[k]
			switch {
			case !inWant:
				return fmt.Sprintf("%s.%s: unexpected key (value %s)", path, k, patchDescribe(gv))
			case !inGot:
				return fmt.Sprintf("%s.%s: key missing (want %s)", path, k, patchDescribe(wv))
			}
			if d := patchStructuralDiff(path+"."+k, wv, gv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: want array, got %s", path, patchDescribe(got))
		}
		if len(w) != len(g) {
			return fmt.Sprintf("%s: want %d elements, got %d", path, len(w), len(g))
		}
		for i := range w {
			if d := patchStructuralDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return fmt.Sprintf("%s: want number %s, got %s", path, w.String(), patchDescribe(got))
		}
		if w.String() != g.String() {
			return fmt.Sprintf("%s: want number %s, got %s (decimal text differs)", path, w.String(), g.String())
		}
		return ""
	default:
		if want != got {
			return fmt.Sprintf("%s: want %s, got %s", path, patchDescribe(want), patchDescribe(got))
		}
		return ""
	}
}

func patchDescribe(v any) string {
	switch x := v.(type) {
	case nil:
		return "absent/null"
	case json.Number:
		return "number " + x.String()
	case string:
		return fmt.Sprintf("string %q", x)
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return fmt.Sprintf("%T %v", v, v)
	}
}

func captureLoaderStderr(t *testing.T, into *bytes.Buffer, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(into, r)
	}()
	defer func() {
		os.Stderr = orig
		_ = w.Close()
		<-done
		_ = r.Close()
	}()
	fn()
}
