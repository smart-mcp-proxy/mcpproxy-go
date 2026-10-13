package oauth

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #1148, round 7. Rounds 4, 5 and 6 each found the GUARD failing open
// rather than the code it guards: a hand-maintained marker list that did not
// know about one of this package's own mask renderings, a field list that
// covered only the fields somebody remembered, and a reflection guard that
// walked only the TOP level of a struct whose credentials live two levels down.
//
// The answers here are structural: the marker set is DERIVED from the renderers
// (maskRenderings), and the decision guard RECURSES.

// ROUND 7, FINDING 1 — every rendering this package produces for a masked
// secret must be recognised by the fail-closed net.
//
// `***REDACTED***` (RedactSensitiveData) was a live read-door rendering that
// MaskMarkers did not list, so CheckServerWriteMasks accepted an echo of it and
// persisted the mask over the real credential.
func TestMaskMarkers_CoverEveryRendering(t *testing.T) {
	require.NotEmpty(t, maskRenderings, "the renderer registry must not be empty")
	for _, r := range maskRenderings {
		t.Run(r.name, func(t *testing.T) {
			got := r.render(r.probe)
			require.NotEqual(t, r.probe, got,
				"probe %q is not masked by %s — the registry entry proves nothing", r.probe, r.name)
			assert.Contains(t, got, r.marker,
				"%s no longer renders its declared marker %q: %s", r.name, r.marker, got)
			assert.True(t, ContainsMaskMarker(got),
				"the fail-closed net does not recognise %s's rendering %q — "+
					"MaskMarkers must be derived from the renderers, not maintained beside them",
				r.name, got)
		})
	}
}

// And the net that matters: a write door must REFUSE any of those renderings,
// wherever in the payload it appears.
func TestCheckServerWriteMasks_RefusesEveryRendering(t *testing.T) {
	for _, r := range maskRenderings {
		t.Run(r.name, func(t *testing.T) {
			payload := map[string]interface{}{
				"isolation": map[string]interface{}{
					"extra_args": []interface{}{"-e", r.render(r.probe)},
				},
			}
			require.Error(t, CheckServerWriteMasks("server.", payload),
				"an echoed %s rendering was ACCEPTED and would be persisted over the credential", r.name)
		})
	}
}

// The end-to-end shape the reviewer reproduced: read the isolation block off a
// live read door, echo it straight back at a write door.
func TestIsolationExtraArgs_MaskEchoIsRefused(t *testing.T) {
	stored := &config.ServerConfig{
		Name:      "srv",
		Command:   "uvx",
		Isolation: &config.IsolationConfig{ExtraArgs: []string{"-e", "API_KEY=" + ghpToken}},
	}
	view, ok := NormalizeForRedaction(stored).(map[string]interface{})
	require.True(t, ok)
	redacted, ok := LiveRedaction.Value("", view).(map[string]interface{})
	require.True(t, ok)

	iso, ok := redacted["isolation"].(map[string]interface{})
	require.True(t, ok, "isolation must survive the walk: %#v", redacted)
	args, ok := iso["extra_args"].([]interface{})
	require.True(t, ok)
	for _, a := range args {
		assert.NotContains(t, a.(string), ghpToken, "the read door published the credential: %v", args)
	}

	require.Error(t, CheckServerWriteMasks("server.", redacted),
		"echoing the redacted isolation block back must be refused, not persisted")
}

// ROUND 7, FINDING 2 — RedactServerSecretFields (the REST + SSE door) skipped
// server.Isolation entirely, so `isolation.extra_args` was masked on the MCP
// door and published in the clear on GET /api/v1/servers and every
// /events servers.changed payload.
func TestRedactServerSecretFields_MasksIsolation(t *testing.T) {
	srv := &contracts.Server{
		Name:              "s",
		Isolation:         &contracts.IsolationConfig{ExtraArgs: []string{"-e", "API_KEY=" + ghpToken}},
		IsolationDefaults: &contracts.IsolationDefaults{ExtraArgs: []string{"-e", "API_KEY=" + ghpToken}},
	}
	RedactServerSecretFields(srv)

	for _, got := range srv.Isolation.ExtraArgs {
		assert.NotContains(t, got, ghpToken, "isolation.extra_args published verbatim: %v", srv.Isolation.ExtraArgs)
	}
	for _, got := range srv.IsolationDefaults.ExtraArgs {
		assert.NotContains(t, got, ghpToken,
			"isolation_defaults.extra_args published verbatim: %v", srv.IsolationDefaults.ExtraArgs)
	}
}

// Redacting a nested block must never write back through a slice the payload
// still shares with the stored config. encoding/json reuses an existing slice's
// backing array, so decoding the mask straight over the projection would
// OVERWRITE the live credential with its own mask — the #1142/#1146 corruption
// this branch exists to prevent, reintroduced by the act of rendering a read.
func TestRedactServerSecretFields_DoesNotWriteBackThroughASharedSlice(t *testing.T) {
	shared := []string{"-e", "API_KEY=" + ghpToken}
	srv := &contracts.Server{
		Name:              "s",
		Isolation:         &contracts.IsolationConfig{ExtraArgs: shared},
		IsolationDefaults: &contracts.IsolationDefaults{ExtraArgs: shared},
	}
	RedactServerSecretFields(srv)

	assert.Equal(t, []string{"-e", "API_KEY=" + ghpToken}, shared,
		"redacting a read payload must not mutate the slice it was built from")
	assert.NotContains(t, srv.Isolation.ExtraArgs[1], ghpToken)
}

// The general form of finding 2, derived from the decision table rather than
// from a list of fields somebody remembered: fill EVERY text-carrying leaf of
// contracts.Server with a credential, redact, and require that the only places
// it survives are the ones explicitly recorded as not-secret.
func TestRedactServerSecretFields_MasksEverySecretBearingLeaf(t *testing.T) {
	srv := &contracts.Server{}
	fillTextLeaves(reflect.ValueOf(srv).Elem(), ghpToken)
	RedactServerSecretFields(srv)

	for _, path := range findTokenPaths(t, srv, ghpToken) {
		decision := decisionForPath(path)
		assert.Equal(t, MaskDecisionNotSecret, decision,
			"contracts.Server.%s published the credential verbatim, but its decision is %q — "+
				"a field that can carry a secret must be masked on this door too", path, decision)
	}
}

// ROUND 7, FINDING 4 — the decision guard walked only TOP-LEVEL fields, so
// every credential-bearing leaf that lives in a nested struct (isolation,
// isolation_defaults, oauth's own leaves) inherited "no decision" silently.
func TestServerFieldMaskDecisions_CoverEveryNestedLeaf(t *testing.T) {
	for _, subject := range serverWireTypes() {
		t.Run(subject.name, func(t *testing.T) {
			for _, path := range textLeafPaths(subject.typ) {
				_, ok := ServerFieldMaskDecisions[path]
				require.True(t, ok,
					"%s carries text at %q with no entry in oauth.ServerFieldMaskDecisions.\n"+
						"Every credential-bearing leaf — nested ones included — needs an answer to: when a client "+
						"echoes this field's mask back on a write, is it REVERTED (bound to which key?) or REFUSED?",
					subject.name, path)
			}
		})
	}
}

// The guard is only worth having if it actually fails on a nested addition.
// This is the "temporarily add a nested field" check the reviewer asked for,
// expressed as a permanent test: a synthetic struct with a nested credential
// leaf must be reported as needing a decision.
func TestTextLeafPaths_ReachesNestedStructsSlicesAndMaps(t *testing.T) {
	type deep struct {
		Token string `json:"token"`
	}
	type mid struct {
		Deep     *deep             `json:"deep"`
		List     []deep            `json:"list"`
		ByName   map[string]deep   `json:"by_name"`
		Strings  []string          `json:"strings"`
		StrByKey map[string]string `json:"str_by_key"`
		Count    int               `json:"count"`
		When     time.Time         `json:"when"`
	}
	type root struct {
		Mid    mid  `json:"mid"`
		Toggle bool `json:"toggle"`
	}

	got := textLeafPaths(reflect.TypeOf(root{}))
	for _, want := range []string{
		"mid.deep.token",
		"mid.list[].token",
		"mid.by_name{}.token",
		"mid.strings",
		"mid.str_by_key",
	} {
		assert.Contains(t, got, want, "the walk must reach %s", want)
	}
	for _, notWanted := range []string{"toggle", "mid.count", "mid.when"} {
		assert.NotContains(t, got, notWanted,
			"%s cannot carry text, so demanding a decision for it is noise", notWanted)
	}
}

// A table entry that names nothing on the wire reads as a decision being
// enforced somewhere when it is not.
func TestServerFieldMaskDecisions_HaveNoStaleNestedEntries(t *testing.T) {
	onTheWire := map[string]bool{}
	for _, subject := range serverWireTypes() {
		for _, path := range allWirePaths(subject.typ) {
			onTheWire[path] = true
		}
	}
	for name := range ServerFieldMaskDecisions {
		assert.True(t, onTheWire[name],
			"oauth.ServerFieldMaskDecisions has a decision for %q, which names no field of "+
				"config.ServerConfig or contracts.Server", name)
	}
}

// decisionForPath resolves the decision for a leaf, falling back to its nearest
// recorded ancestor so a container marked not-secret answers for its leaves.
func decisionForPath(path string) MaskDecision {
	for {
		if d, ok := ServerFieldMaskDecisions[path]; ok {
			return d
		}
		cut := strings.LastIndexAny(path, ".[{")
		if cut < 0 {
			return MaskDecision("<none>")
		}
		path = path[:cut]
	}
}

// fillTextLeaves writes `token` into every string leaf reachable from v,
// allocating pointers, one-element slices and one-entry maps on the way.
func fillTextLeaves(v reflect.Value, token string) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(token)
	case reflect.Ptr:
		if v.Type().Elem() == reflect.TypeOf(time.Time{}) {
			return
		}
		if !carriesText(v.Type().Elem()) {
			return
		}
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fillTextLeaves(v.Elem(), token)
	case reflect.Slice:
		if !carriesText(v.Type().Elem()) {
			return
		}
		elem := reflect.New(v.Type().Elem()).Elem()
		fillTextLeaves(elem, token)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), elem))
	case reflect.Map:
		if !carriesText(v.Type().Elem()) {
			return
		}
		elem := reflect.New(v.Type().Elem()).Elem()
		fillTextLeaves(elem, token)
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.ValueOf("BENIGN").Convert(v.Type().Key()), elem)
		v.Set(m)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			if name := jsonFieldName(v.Type().Field(i)); name == "" || name == "-" {
				continue
			}
			fillTextLeaves(v.Field(i), token)
		}
	}
}

// findTokenPaths returns the dotted wire paths where `token` still appears.
func findTokenPaths(t *testing.T, v interface{}, token string) []string {
	t.Helper()
	var out []string
	var walk func(path string, node interface{})
	walk = func(path string, node interface{}) {
		switch typed := node.(type) {
		case map[string]interface{}:
			for k, val := range typed {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(child, val)
			}
		case []interface{}:
			for _, val := range typed {
				walk(path, val)
			}
		case string:
			if strings.Contains(typed, token) {
				out = append(out, path)
			}
		}
	}
	walk("", NormalizeForRedaction(v))
	return out
}
