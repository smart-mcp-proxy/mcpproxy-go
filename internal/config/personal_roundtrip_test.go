//go:build !server

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec 107 FR-040 (T010, personal build): the `server_edition` block and every
// server's `auth_broker` block must survive load → save → load → save
// semantically intact — every key and every value, numbers by their decimal
// text — with no warning emitted. On HEAD both stubs are `struct{}`, so the
// personal binary writes `{}` back for each block (critic G3); this test is
// red until T020 lands the json.RawMessage carriers.
//
// The comparator is deliberately independent of the FR-015 canonicaliser: it
// decodes both documents with UseNumber and compares the trees structurally,
// so the planted 9007199254740993 (2^53+1) and
// 0.1000000000000000055511151231257827 cannot pass through a float64 unnoticed.

const (
	legacyFixturePath = "testdata/legacy_server_edition.json"

	// Probe keys planted into every opaque block. They are unknown to the
	// server-build decoders, which is fine: the personal build must carry
	// them through untouched.
	probeIntKey = "roundtrip_probe_int"
	probeDecKey = "roundtrip_probe_dec"
	probeInt    = "9007199254740993"
	probeDec    = "0.1000000000000000055511151231257827"
)

// TestPersonalBuild_OpaqueBlocksRoundTrip is the load → save → load → save
// leg of FR-040. The PATCH-merge leg (which needs httpapi.MergeConfigPatch,
// T020) lives in internal/httpapi/config_patch_roundtrip_test.go.
func TestPersonalBuild_OpaqueBlocksRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original, planted := plantRoundTripProbes(t, legacyFixturePath, dir)

	src := filepath.Join(dir, "planted.json")
	require.NoError(t, os.WriteFile(src, planted, 0o600))

	var stderr bytes.Buffer
	firstSave := filepath.Join(dir, "after_first_save.json")
	secondSave := filepath.Join(dir, "after_second_save.json")
	captureStderr(t, &stderr, func() {
		cfg, err := LoadFromFile(src)
		require.NoError(t, err)
		require.NoError(t, SaveConfig(cfg, firstSave))

		reloaded, err := LoadFromFile(firstSave)
		require.NoError(t, err)
		require.NoError(t, SaveConfig(reloaded, secondSave))
	})
	require.NotContains(t, stderr.String(), "WARN", "the personal build must not warn about the opaque blocks")

	for _, path := range []string{firstSave, secondSave} {
		saved, err := os.ReadFile(path)
		require.NoError(t, err)
		assertOpaqueBlocksEqual(t, original, saved, filepath.Base(path))
	}
}

// plantRoundTripProbes reads the shared fixture, plants the precision probes
// into `server_edition` and every `mcpServers[].auth_broker`, points data_dir
// at a temp dir, and returns (original tree, planted document bytes).
func plantRoundTripProbes(t *testing.T, fixture, dataDir string) (map[string]any, []byte) {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	require.NoError(t, err, "shared fixture %s (created by T009) must exist", fixture)

	doc := decodeUseNumber(t, raw)
	doc["data_dir"] = filepath.ToSlash(dataDir)

	se, ok := doc["server_edition"].(map[string]any)
	require.True(t, ok, "fixture must carry a server_edition object")
	se[probeIntKey] = json.Number(probeInt)
	se[probeDecKey] = json.Number(probeDec)

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
		broker[probeIntKey] = json.Number(probeInt)
		broker[probeDecKey] = json.Number(probeDec)
		planted++
	}
	require.Greater(t, planted, 0, "fixture must carry at least one mcpServers[].auth_broker object")

	// json.Number marshals verbatim, so the planted text survives this write.
	out, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	return decodeUseNumber(t, out), out
}

// assertOpaqueBlocksEqual compares server_edition and every auth_broker block
// (keyed by server name) between the original tree and a saved document.
func assertOpaqueBlocksEqual(t *testing.T, original map[string]any, saved []byte, label string) {
	t.Helper()
	got := decodeUseNumber(t, saved)

	if diff := structuralDiff("server_edition", original["server_edition"], got["server_edition"]); diff != "" {
		t.Errorf("%s: server_edition block changed on personal write-back: %s", label, diff)
	}

	wantBrokers := authBrokersByName(t, original)
	gotBrokers := authBrokersByName(t, got)
	names := make([]string, 0, len(wantBrokers))
	for name := range wantBrokers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if diff := structuralDiff("mcpServers["+name+"].auth_broker", wantBrokers[name], gotBrokers[name]); diff != "" {
			t.Errorf("%s: auth_broker block for server %q changed on personal write-back: %s", label, name, diff)
		}
	}
}

func authBrokersByName(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
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

func decodeUseNumber(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	require.NoError(t, dec.Decode(&doc))
	return doc
}

// structuralDiff is the independent comparator: maps by key set and value,
// arrays by position, json.Number by decimal text, everything else by ==.
// It returns "" when equal, otherwise a path-qualified description.
func structuralDiff(path string, want, got any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: want object, got %s", path, describe(got))
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
				return fmt.Sprintf("%s.%s: unexpected key (value %s)", path, k, describe(gv))
			case !inGot:
				return fmt.Sprintf("%s.%s: key missing (want %s)", path, k, describe(wv))
			}
			if d := structuralDiff(path+"."+k, wv, gv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: want array, got %s", path, describe(got))
		}
		if len(w) != len(g) {
			return fmt.Sprintf("%s: want %d elements, got %d", path, len(w), len(g))
		}
		for i := range w {
			if d := structuralDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); d != "" {
				return d
			}
		}
		return ""
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return fmt.Sprintf("%s: want number %s, got %s", path, w.String(), describe(got))
		}
		if w.String() != g.String() {
			return fmt.Sprintf("%s: want number %s, got %s (decimal text differs)", path, w.String(), g.String())
		}
		return ""
	default:
		if want != got {
			return fmt.Sprintf("%s: want %s, got %s", path, describe(want), describe(got))
		}
		return ""
	}
}

func describe(v any) string {
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

// captureStderr redirects os.Stderr for the duration of fn (the loader has no
// logger; its warnings are fmt.Fprintf(os.Stderr, "WARN: ...") lines).
func captureStderr(t *testing.T, into *bytes.Buffer, fn func()) {
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
	func() {
		defer func() {
			os.Stderr = orig
			_ = w.Close()
			<-done
			_ = r.Close()
		}()
		fn()
	}()
	if s := into.String(); s != "" {
		t.Logf("stderr during load/save:\n%s", strings.TrimSpace(s))
	}
}
