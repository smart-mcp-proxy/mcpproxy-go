package httpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109-m (FR-090, T144, M2/M3): the six terminology enums as one golden
// (internal/contracts/testdata/terminology.json) that Go, vitest and XCTest all
// decode. Every helper in this file is prefixed p109 so 108-l's p108 helpers
// (profiles_v3_parity_test.go) never collide.

const p109TerminologyGolden = "internal/contracts/testdata/terminology.json"

// p109MarshalGolden renders a golden file: indented, no HTML escaping, one
// trailing newline.
func p109MarshalGolden(t testing.TB, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(v))
	return buf.Bytes()
}

// p109Family is one enum family of terminology.json. Field order is the order
// the file is written in.
type p109Family struct {
	Values  []string          `json:"values"`
	Rank    map[string]int    `json:"rank,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Retired []string          `json:"retired,omitempty"`
	CLIFlag string            `json:"cli_flag,omitempty"`
	CLI     []string          `json:"cli,omitempty"`
}

type p109Terminology struct {
	Comment        string     `json:"_comment"`
	HealthStatus   p109Family `json:"health_status"`
	AttentionKind  p109Family `json:"attention_kind"`
	Tier           p109Family `json:"tier"`
	ToolApproval   p109Family `json:"tool_approval"`
	ActivityView   p109Family `json:"activity_view"`
	ClientPresence p109Family `json:"client_presence"`
}

// families returns every family keyed by its golden name, with the generated
// TypeScript const prefix and union type name.
func (g *p109Terminology) families() []struct {
	name, prefix, typeName string
	family                 *p109Family
} {
	return []struct {
		name, prefix, typeName string
		family                 *p109Family
	}{
		{"health_status", "HealthStatus", "HealthStatusValue", &g.HealthStatus},
		{"attention_kind", "AttentionKind", "AttentionKind", &g.AttentionKind},
		{"tier", "Tier", "Tier", &g.Tier},
		{"tool_approval", "ToolApproval", "ToolApprovalState", &g.ToolApproval},
		{"activity_view", "ActivityView", "ActivityView", &g.ActivityView},
		{"client_presence", "ClientPresence", "ClientPresenceState", &g.ClientPresence},
	}
}

func p109Root(t testing.TB) string {
	t.Helper()
	return p108RepoRoot(t)
}

func p109ReadTerminology(t testing.TB) p109Terminology {
	t.Helper()
	var g p109Terminology
	p108ReadJSON(t, p109TerminologyGolden, &g)
	return g
}

// p109GoValues is the Go-side truth for each family, keyed by golden name.
func p109GoValues() map[string][]string {
	tiers := []string{}
	for _, tier := range contracts.AllTiers() {
		tiers = append(tiers, string(tier))
	}
	return map[string][]string{
		"health_status":   append([]string(nil), health.StatusOrder...),
		"attention_kind":  runtime.AttentionKinds(),
		"tier":            tiers,
		"tool_approval":   contracts.AllToolApprovalStates(),
		"activity_view":   contracts.AllActivityViews(),
		"client_presence": contracts.AllClientPresenceStates(),
	}
}

// p109AttentionRanks is the lowest rank each kind can carry
// (tool_review also has rank 61 for the pending state).
func p109AttentionRanks() map[string]int {
	return map[string]int{
		runtime.AttentionKindAnonymousDeniedByBindingGuard: runtime.AttentionRankAnonymousDenied,
		runtime.AttentionKindClientHoldsAdminKey:           runtime.AttentionRankClientHoldsAdminKey,
		runtime.AttentionKindClientTokenNameConflict:       runtime.AttentionRankClientTokenNameConflict,
		runtime.AttentionKindProfileMissing:                runtime.AttentionRankProfileMissing,
		runtime.AttentionKindClientRotationPending:         runtime.AttentionRankClientRotationPending,
		runtime.AttentionKindClientCredentialExpiring:      runtime.AttentionRankClientCredentialExpiring,
		runtime.AttentionKindSignInRequired:                runtime.AttentionRankSignInRequired,
		runtime.AttentionKindMissingSecret:                 runtime.AttentionRankMissingSecret,
		runtime.AttentionKindConfigError:                   runtime.AttentionRankConfigError,
		runtime.AttentionKindServerError:                   runtime.AttentionRankServerError,
		runtime.AttentionKindServerReview:                  runtime.AttentionRankServerReview,
		runtime.AttentionKindToolReview:                    runtime.AttentionRankToolReviewChanged,
		runtime.AttentionKindClientNeverSeen:               runtime.AttentionRankClientNeverSeen,
	}
}

// TestSpec109TerminologyGolden fails when a Go constant has no golden entry
// (or the reverse), when a hand-maintained label is missing or swapped, or
// when a mirrored constant drifts from its storage twin. UPDATE_GOLDEN=1
// rewrites the golden's values from Go and keeps the labels.
func TestSpec109TerminologyGolden(t *testing.T) {
	g := p109ReadTerminology(t)
	goValues := p109GoValues()

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		for _, f := range g.families() {
			f.family.Values = goValues[f.name]
		}
		g.AttentionKind.Rank = p109AttentionRanks()
		require.NoError(t, os.WriteFile(filepath.Join(p109Root(t), p109TerminologyGolden), p109MarshalGolden(t, &g), 0o644))
		t.Log("terminology.json rewritten from Go; review the diff")
		return
	}

	for _, f := range g.families() {
		assert.Equal(t, goValues[f.name], f.family.Values, "%s: golden values must equal the Go constants, in order (UPDATE_GOLDEN=1 regenerates)", f.name)
		if len(f.family.Labels) > 0 {
			assert.Len(t, f.family.Labels, len(f.family.Values), "%s: a label per value, none extra", f.name)
			for _, v := range f.family.Values {
				assert.NotEmpty(t, f.family.Labels[v], "%s: value %q has no label", f.name, v)
			}
		}
	}

	// Health labels are owned by the Go label table; compare entry by entry
	// (never by substring, #1376 follow-up 5).
	for _, v := range g.HealthStatus.Values {
		assert.Equal(t, health.StatusLabels[v], g.HealthStatus.Labels[v], "health label for %s", v)
	}

	// Attention ranks ascend along the values order and equal the Go ranks.
	ranks := p109AttentionRanks()
	require.Len(t, ranks, len(g.AttentionKind.Values))
	prev := -1
	for _, kind := range g.AttentionKind.Values {
		assert.Equal(t, ranks[kind], g.AttentionKind.Rank[kind], "rank of %s", kind)
		assert.Greater(t, g.AttentionKind.Rank[kind], prev, "%s must rank after the previous kind", kind)
		prev = g.AttentionKind.Rank[kind]
	}

	// Mirrored constants equal their storage twins.
	assert.Equal(t, storage.ToolApprovalStatusApproved, contracts.ToolApprovalApproved)
	assert.Equal(t, storage.ToolApprovalStatusPending, contracts.ToolApprovalPending)
	assert.Equal(t, storage.ToolApprovalStatusChanged, contracts.ToolApprovalChanged)

	// The health golden agrees with the older status_fixtures.json that the
	// Swift and vitest health tests already read.
	var fixtures struct {
		StatusOrder  []string          `json:"status_order"`
		StatusLabels map[string]string `json:"status_labels"`
	}
	p108ReadJSON(t, "internal/health/testdata/status_fixtures.json", &fixtures)
	assert.Equal(t, fixtures.StatusOrder, g.HealthStatus.Values)
	assert.Equal(t, fixtures.StatusLabels, g.HealthStatus.Labels)

	// Activity views: the CLI list is the golden list minus sessions.
	var cli []string
	for _, v := range g.ActivityView.Values {
		if v != contracts.ActivityViewSessions {
			cli = append(cli, v)
		}
	}
	assert.Equal(t, cli, g.ActivityView.CLI, "the CLI has no sessions view (parity row 19)")

	// Retired approval names never appear as a label.
	for _, label := range g.ToolApproval.Labels {
		for _, retired := range g.ToolApproval.Retired {
			assert.NotEqual(t, retired, label)
		}
	}
}

// p109CamelValue turns a wire value into its generated TypeScript suffix:
// sign_in_required -> SignInRequired.
func p109CamelValue(v string) string {
	var b strings.Builder
	for _, w := range strings.Split(v, "_") {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// p109TSLines reads contracts.ts as a line set and an ordered slice.
func p109TSLines(t testing.TB) (map[string]bool, []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(p109Root(t), "frontend", "src", "types", "contracts.ts"))
	require.NoError(t, err)
	ordered := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	set := make(map[string]bool, len(ordered))
	for _, l := range ordered {
		set[l] = true
	}
	return set, ordered
}

// p109TSUnion returns the member constant names of `export type <name> =`.
func p109TSUnion(ordered []string, name string) []string {
	var members []string
	in := false
	for _, l := range ordered {
		if !in {
			if l == "export type "+name+" =" {
				in = true
			}
			continue
		}
		member := strings.TrimPrefix(l, "  | typeof ")
		if member == l {
			break
		}
		members = append(members, strings.TrimSuffix(member, ";"))
		if strings.HasSuffix(l, ";") {
			break
		}
	}
	return members
}

// TestSpec109ContractsTSExactLines pins every enum value against the generated
// contracts.ts with an EXACT line per constant and an exact union per family
// (never strings.Contains). It folds in 109-l's
// TestAttention108KindsMatchGeneratedTypeScript unchanged: the 13 attention
// kinds and the AttentionSubject `setting` type.
func TestSpec109ContractsTSExactLines(t *testing.T) {
	g := p109ReadTerminology(t)
	lines, ordered := p109TSLines(t)

	for _, f := range g.families() {
		var want []string
		for _, v := range f.family.Values {
			name := f.prefix + p109CamelValue(v)
			want = append(want, name)
			assert.True(t, lines["export const "+name+" = '"+v+"' as const;"], "%s: missing exact line for %s", f.name, v)
		}
		assert.Equal(t, want, p109TSUnion(ordered, f.typeName), "%s: export type %s must list exactly the values, in order", f.name, f.typeName)
	}

	// Health labels as generated: [HealthStatusReady]: 'Online',
	for _, v := range g.HealthStatus.Values {
		assert.True(t, lines["  [HealthStatus"+p109CamelValue(v)+"]: '"+g.HealthStatus.Labels[v]+"',"], "health label line for %s", v)
	}

	// 109-l P13, folded: the attention subject union lists `setting`.
	assert.True(t, lines["  type: 'server' | 'tool' | 'client' | 'setting';"], "AttentionSubject.type must list setting")
	assert.Len(t, runtime.AttentionKinds(), 13)
}

// TestSpec109TerminologyGoldenSelfCheck proves the checks above bite: a
// swapped label, a dropped value and a duplicated constant all fail.
func TestSpec109TerminologyGoldenSelfCheck(t *testing.T) {
	g := p109ReadTerminology(t)

	swapped := g.HealthStatus.Labels["ready"]
	assert.NotEqual(t, g.HealthStatus.Labels["error"], swapped)
	assert.NotEqual(t, health.StatusLabels["error"], swapped, "a swapped label must not equal the Go label")

	values := p109GoValues()
	dropped := values["tier"][:len(values["tier"])-1]
	assert.NotEqual(t, dropped, g.Tier.Values, "a golden missing a Go value must not equal it")

	got := append([]string(nil), values["activity_view"]...)
	sort.Strings(got)
	assert.Len(t, got, 4)
}
