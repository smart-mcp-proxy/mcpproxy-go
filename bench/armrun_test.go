package bench

// armrun_test.go — T024: arm runner + ArmResult assembly over a small
// in-test corpus (FR-005/008/009/020). Fake arms keep the tests hermetic;
// the real-arm integration path is covered by bench/arms contract tests and
// the SC-003 parity gate in armindex_test.go.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// fakeArm is a scriptable EncodingArm for runner tests.
type fakeArm struct {
	name          string
	indexAltering bool
	lowerBound    bool
	encode        func(t Tool) (string, error)
	meta          func(t Tool) (config.ToolMetadata, error)
}

func (f *fakeArm) Name() string        { return f.name }
func (f *fakeArm) IndexAltering() bool { return f.indexAltering }
func (f *fakeArm) LowerBound() bool    { return f.lowerBound }

func (f *fakeArm) EncodeTool(t Tool) (string, error) { return f.encode(t) }

func (f *fakeArm) EncodeListing(ts []Tool) (string, error) {
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		enc, err := f.encode(t)
		if err != nil {
			return "", err
		}
		parts = append(parts, enc)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (f *fakeArm) EncodeIndexMetadata(t Tool) (config.ToolMetadata, error) {
	if f.meta != nil {
		return f.meta(t)
	}
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
		ParamsJSON:  string(t.Schema),
	}, nil
}

// fullEncode is the baseline-shaped rendering (name + description).
func fullEncode(t Tool) (string, error) { return t.Name + "\n" + t.Description, nil }

// newFakeBaseline is the mandatory baseline_json stand-in.
func newFakeBaseline() *fakeArm {
	return &fakeArm{name: "baseline_json", encode: fullEncode}
}

// runnerCorpus is a small deterministic in-test corpus.
func runnerCorpus() *Corpus {
	return &Corpus{
		Version: "corpus_test@1",
		Tools: []Tool{
			{ToolID: "fs:read_file", Server: "fs", Name: "read_file", Description: "Read the contents of a file from disk with optional offset and length"},
			{ToolID: "git:git_log", Server: "git", Name: "git_log", Description: "Show recent commit history of a repository"},
			{ToolID: "time:get_current_time", Server: "time", Name: "get_current_time", Description: "Get current time in a specific timezone"},
		},
	}
}

func runnerTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	return tk
}

func TestRunArms_RequiresBaseline(t *testing.T) {
	tk := runnerTokenizer(t)
	short := &fakeArm{name: "short_arm", encode: func(t Tool) (string, error) { return t.Name, nil }}
	if _, err := RunArms(tk, runnerCorpus(), []EncodingArm{short}, ArmRunOptions{}); err == nil {
		t.Fatal("RunArms without the baseline_json arm must fail: savings have no denominator")
	}
}

func TestRunArms_EmptyCorpus(t *testing.T) {
	tk := runnerTokenizer(t)
	if _, err := RunArms(tk, &Corpus{Version: "empty"}, []EncodingArm{newFakeBaseline()}, ArmRunOptions{}); err == nil {
		t.Fatal("RunArms over an empty corpus must fail")
	}
}

func TestRunArms_TokenStatsAndSavings(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := runnerCorpus()
	baseline := newFakeBaseline()
	short := &fakeArm{
		name:          "short_arm",
		indexAltering: true,
		lowerBound:    true,
		encode:        func(t Tool) (string, error) { return t.Name, nil }, // drops descriptions
	}

	results, err := RunArms(tk, corpus, []EncodingArm{baseline, short}, ArmRunOptions{})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	base, arm := results[0], results[1]
	if base.Arm != "baseline_json" || arm.Arm != "short_arm" {
		t.Fatalf("results out of caller order: %s, %s", base.Arm, arm.Arm)
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("%s: unexpectedly skipped: %s", r.Arm, r.SkipReason)
		}
		if r.CorpusID != corpus.Version {
			t.Errorf("%s: CorpusID = %q, want default corpus version %q", r.Arm, r.CorpusID, corpus.Version)
		}
		if r.PayloadClass != "listing" {
			t.Errorf("%s: PayloadClass = %q, want listing", r.Arm, r.PayloadClass)
		}
		if r.SkippedTools != 0 || len(r.SkipExamples) != 0 {
			t.Errorf("%s: unexpected skips: %d (%v)", r.Arm, r.SkippedTools, r.SkipExamples)
		}
	}

	// Flags propagate from the arm.
	if base.IndexAltering || base.LowerBound {
		t.Error("baseline flags must be false")
	}
	if !arm.IndexAltering || !arm.LowerBound {
		t.Error("short_arm flags must propagate (IndexAltering=true, LowerBound=true)")
	}

	// TotalTokens is the amortized whole-listing cost.
	listing, err := baseline.EncodeListing(corpus.Tools)
	if err != nil {
		t.Fatalf("EncodeListing: %v", err)
	}
	if want := tk.Count(listing); base.TotalTokens != want {
		t.Errorf("baseline TotalTokens = %d, want tk.Count(listing) = %d", base.TotalTokens, want)
	}

	// Mean/P95 come from per-tool encodings (contract rule 6).
	sum := 0
	maxTok := 0
	for _, tl := range corpus.Tools {
		enc, eerr := baseline.EncodeTool(tl)
		if eerr != nil {
			t.Fatalf("EncodeTool: %v", eerr)
		}
		n := tk.Count(enc)
		sum += n
		if n > maxTok {
			maxTok = n
		}
	}
	if want := float64(sum) / float64(len(corpus.Tools)); base.MeanTokens != want {
		t.Errorf("baseline MeanTokens = %v, want %v", base.MeanTokens, want)
	}
	if base.P95Tokens != maxTok {
		t.Errorf("baseline P95Tokens = %d, want max %d (nearest-rank over 3 samples)", base.P95Tokens, maxTok)
	}

	// Savings: baseline is the denominator (0%); the name-only arm saves.
	if base.SavingsVsBaselinePct != 0 {
		t.Errorf("baseline SavingsVsBaselinePct = %v, want 0", base.SavingsVsBaselinePct)
	}
	if arm.SavingsVsBaselinePct <= 0 {
		t.Errorf("short_arm SavingsVsBaselinePct = %v, want > 0 (encodes names only)", arm.SavingsVsBaselinePct)
	}
	wantSavings := (1 - float64(arm.TotalTokens)/float64(base.TotalTokens)) * 100
	if diff := arm.SavingsVsBaselinePct - wantSavings; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("short_arm savings = %v, want %v", arm.SavingsVsBaselinePct, wantSavings)
	}
}

func TestRunArms_SkipCountingWithExamples(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := runnerCorpus()
	flaky := &fakeArm{
		name: "flaky_arm",
		encode: func(t Tool) (string, error) {
			if t.ToolID == "git:git_log" {
				return "", fmt.Errorf("unencodable schema shape")
			}
			return t.Name, nil
		},
	}

	results, err := RunArms(tk, corpus, []EncodingArm{newFakeBaseline(), flaky}, ArmRunOptions{})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}
	arm := results[1]
	if arm.SkippedTools != 1 {
		t.Fatalf("SkippedTools = %d, want 1", arm.SkippedTools)
	}
	if len(arm.SkipExamples) != 1 {
		t.Fatalf("SkipExamples = %v, want exactly 1", arm.SkipExamples)
	}
	ex := arm.SkipExamples[0]
	if ex.ToolID != "git:git_log" {
		t.Errorf("SkipExamples[0].ToolID = %q, want git:git_log", ex.ToolID)
	}
	if !strings.Contains(ex.Error, "unencodable schema shape") {
		t.Errorf("SkipExamples[0].Error = %q, want the encode error text", ex.Error)
	}

	// The skipped tool is excluded from per-tool stats and the listing total.
	wantMean := float64(tk.Count("read_file")+tk.Count("get_current_time")) / 2
	if arm.MeanTokens != wantMean {
		t.Errorf("MeanTokens = %v, want %v (skipped tool excluded)", arm.MeanTokens, wantMean)
	}
	if want := tk.Count("read_file\n\nget_current_time"); arm.TotalTokens != want {
		t.Errorf("TotalTokens = %d, want %d (listing over encodable tools only)", arm.TotalTokens, want)
	}
	if len(arm.HeaviestTools) != 2 {
		t.Errorf("HeaviestTools has %d entries, want 2 (skipped tool excluded)", len(arm.HeaviestTools))
	}
}

func TestRunArms_SkipExamplesCapped(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := &Corpus{Version: "big"}
	for i := 0; i < 8; i++ {
		corpus.Tools = append(corpus.Tools, Tool{
			ToolID: fmt.Sprintf("s:t%02d", i), Server: "s", Name: fmt.Sprintf("t%02d", i),
			Description: "a description long enough to not be degenerate",
		})
	}
	broken := &fakeArm{
		name: "broken_arm",
		encode: func(t Tool) (string, error) {
			if t.ToolID == "s:t07" {
				return t.Name, nil
			}
			return "", fmt.Errorf("boom on %s", t.ToolID)
		},
	}

	results, err := RunArms(tk, corpus, []EncodingArm{newFakeBaseline(), broken}, ArmRunOptions{})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}
	arm := results[1]
	if arm.SkippedTools != 7 {
		t.Errorf("SkippedTools = %d, want 7", arm.SkippedTools)
	}
	if len(arm.SkipExamples) != maxSkipExamples {
		t.Errorf("SkipExamples = %d entries, want capped at %d", len(arm.SkipExamples), maxSkipExamples)
	}
	// Examples are the FIRST failures in corpus order (deterministic).
	if arm.SkipExamples[0].ToolID != "s:t00" {
		t.Errorf("SkipExamples[0] = %q, want s:t00", arm.SkipExamples[0].ToolID)
	}
}

func TestRunArms_HeaviestTopN(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := &Corpus{
		Version: "heavy",
		Tools: []Tool{
			// zz_b and zz_a encode identically → equal tokens → tie broken by tool_id.
			{ToolID: "s:zz_b", Server: "s", Name: "zz_b", Description: "short"},
			{ToolID: "s:zz_a", Server: "s", Name: "zz_a", Description: "short"},
			{ToolID: "s:huge", Server: "s", Name: "huge", Description: "short"},
			{ToolID: "s:tiny", Server: "s", Name: "tiny", Description: "short"},
		},
	}
	arm := &fakeArm{
		name: "weighted_arm",
		encode: func(t Tool) (string, error) {
			switch t.ToolID {
			case "s:huge":
				return strings.Repeat("many different words in a row ", 20), nil
			case "s:tiny":
				return "x", nil
			default:
				return "identical rendering for both zz tools", nil
			}
		},
	}

	results, err := RunArms(tk, corpus, []EncodingArm{newFakeBaseline(), arm}, ArmRunOptions{HeaviestN: 3})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}
	got := results[1].HeaviestTools
	if len(got) != 3 {
		t.Fatalf("HeaviestTools = %d entries, want 3 (HeaviestN)", len(got))
	}
	if got[0].ToolID != "s:huge" {
		t.Errorf("heaviest[0] = %q, want s:huge", got[0].ToolID)
	}
	// Tie: equal tokens ordered by tool_id ascending for determinism.
	if got[1].ToolID != "s:zz_a" || got[2].ToolID != "s:zz_b" {
		t.Errorf("tie-break order = %q, %q; want s:zz_a then s:zz_b", got[1].ToolID, got[2].ToolID)
	}
	if got[0].Tokens < got[1].Tokens || got[1].Tokens != got[2].Tokens {
		t.Errorf("token ordering wrong: %+v", got)
	}
}

// TestRunArms_QualityAttach exercises the armindex path end-to-end: an
// index-altering arm with a golden set gets a real retrieval score; a
// rendering-only arm stays quality-neutral (nil); an index-altering arm
// without labels carries an explanatory MetricNote (FR-008).
func TestRunArms_QualityAttach(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := runnerCorpus()
	golden := &GoldenSet{
		CorpusVersion: "corpus_test@1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "commit history", Labels: []Label{{ToolID: "git:git_log", Relevance: 2}}},
			{ID: "q2", Query: "current time timezone", Labels: []Label{{ToolID: "time:get_current_time", Relevance: 2}}},
		},
	}
	altering := &fakeArm{name: "altering_arm", indexAltering: true, encode: fullEncode}
	neutral := &fakeArm{name: "neutral_arm", encode: fullEncode}

	results, err := RunArms(tk, corpus, []EncodingArm{newFakeBaseline(), altering, neutral},
		ArmRunOptions{Golden: golden})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}

	baseQ := results[0].Quality
	if baseQ != nil {
		t.Errorf("baseline (rendering-only) Quality = %+v, want nil (quality-neutral)", baseQ)
	}
	alt := results[1].Quality
	if alt == nil {
		t.Fatal("index-altering arm with golden set must carry a quality score")
	}
	if alt.RecallAt5 != 1.0 {
		t.Errorf("altering arm recall@5 = %v, want 1.0 (each query's tool is indexed and findable)", alt.RecallAt5)
	}
	if alt.MetricNote == "" {
		t.Error("scored quality must document the gain formula in MetricNote (FR-012)")
	}
	if results[2].Quality != nil {
		t.Errorf("neutral arm Quality = %+v, want nil", results[2].Quality)
	}

	// No golden set: quality key still present, numbers absent, note explains.
	results, err = RunArms(tk, corpus, []EncodingArm{newFakeBaseline(), altering}, ArmRunOptions{})
	if err != nil {
		t.Fatalf("RunArms (no golden): %v", err)
	}
	q := results[1].Quality
	if q == nil {
		t.Fatal("index-altering arm without labels must still carry a quality object with an explanatory note")
	}
	if q.MetricNote == "" || q.RecallAt5 != 0 {
		t.Errorf("unlabeled quality = %+v, want zero metrics + explanatory MetricNote", q)
	}
}

func TestRunArms_Deterministic(t *testing.T) {
	tk := runnerTokenizer(t)
	corpus := runnerCorpus()
	golden := &GoldenSet{
		CorpusVersion: "corpus_test@1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "commit history", Labels: []Label{{ToolID: "git:git_log", Relevance: 2}}},
		},
	}
	mk := func() []EncodingArm {
		return []EncodingArm{
			newFakeBaseline(),
			&fakeArm{name: "altering_arm", indexAltering: true, encode: fullEncode},
		}
	}

	r1, err := RunArms(tk, corpus, mk(), ArmRunOptions{Golden: golden})
	if err != nil {
		t.Fatalf("RunArms (1st): %v", err)
	}
	r2, err := RunArms(tk, corpus, mk(), ArmRunOptions{Golden: golden})
	if err != nil {
		t.Fatalf("RunArms (2nd): %v", err)
	}
	j1, err := json.Marshal(r1)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j2, err := json.Marshal(r2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(j1) != string(j2) {
		t.Errorf("RunArms not deterministic across runs (FR-010):\n%s\n%s", j1, j2)
	}
}

func TestRunArms_CorpusIDOverride(t *testing.T) {
	tk := runnerTokenizer(t)
	results, err := RunArms(tk, runnerCorpus(), []EncodingArm{newFakeBaseline()},
		ArmRunOptions{CorpusID: "corpus_v2@abc123"})
	if err != nil {
		t.Fatalf("RunArms: %v", err)
	}
	if results[0].CorpusID != "corpus_v2@abc123" {
		t.Errorf("CorpusID = %q, want override corpus_v2@abc123", results[0].CorpusID)
	}
}

func TestSkippedArmResult(t *testing.T) {
	r := SkippedArmResult("tscg", "corpus_v2@abc", "node binary not found on PATH")
	if !r.Skipped {
		t.Error("Skipped must be true")
	}
	if r.Arm != "tscg" || r.CorpusID != "corpus_v2@abc" {
		t.Errorf("identity fields wrong: %+v", r)
	}
	if r.SkipReason != "node binary not found on PATH" {
		t.Errorf("SkipReason = %q", r.SkipReason)
	}
}

func TestCountDegenerateDescriptions(t *testing.T) {
	tools := []Tool{
		{ToolID: "s:ok", Description: "A perfectly reasonable description of what this tool does."},
		{ToolID: "s:empty", Description: ""},
		{ToolID: "s:spaces", Description: "   \t\n "},
		{ToolID: "s:short", Description: "Reads the file"}, // 14 chars < 20
		{ToolID: "s:exact20", Description: "12345678901234567890"},
		{ToolID: "s:proxy", Description: "Proxy for `pkg.mod.fn` with plenty of trailing text"},
		{ToolID: "s:proxy_and_short", Description: "Proxy for `x`"}, // trips two rules, counts once
	}

	dd, err := CountDegenerateDescriptions(tools, DefaultStubPatterns())
	if err != nil {
		t.Fatalf("CountDegenerateDescriptions: %v", err)
	}
	if dd.Count != 5 {
		t.Errorf("Count = %d, want 5 (empty, spaces, short, proxy, proxy_and_short)", dd.Count)
	}
	if len(dd.Rules) == 0 {
		t.Error("Rules must echo the applied rule list for reproducibility (FR-020)")
	}
	joined := strings.Join(dd.Rules, " | ")
	for _, frag := range []string{"empty", "20", "^Proxy for "} {
		if !strings.Contains(joined, frag) {
			t.Errorf("Rules %v missing %q", dd.Rules, frag)
		}
	}

	if _, err := CountDegenerateDescriptions(tools, []string{"("}); err == nil {
		t.Error("invalid stub pattern must be an explicit error")
	}
}
