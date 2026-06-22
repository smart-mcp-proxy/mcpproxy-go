// Package contracts validates the Glass Cockpit executionPolicy JSON Schema
// (specs/064-glass-cockpit/contracts/execution-policy.schema.json).
//
// The schema fixes the shape of the gate configuration the cockpit attaches to
// Paperclip issues. This test enforces two things plain JSON Schema cannot, in
// the style of specs/065-evaluation-foundation/datasets/corpus_test.go:
//
//  1. Back-compat: every execution policy that validated before the
//     Reviewer-Liveness Contract was added (i.e. a review/approval stage with no
//     `liveness` block) still validates unchanged.
//  2. The Reviewer-Liveness Contract (FR-014a, mirroring the MCP-3066 shell
//     backstop ~/.mcpproxy-gatekeeper/bin/ensure-pr-gates.sh): a review stage MAY
//     carry a `liveness` block with a model-diverse fallback roster, an SLA, and
//     a per-head re-trigger budget; the same-family exclusion (a roster reviewer
//     must not share the primary reviewer's modelFamily) is a cross-entity
//     invariant checked here in Go.
package contracts

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaFile  = "execution-policy.schema.json"
	exampleFile = "reviewer-liveness.example.json"
)

// Contract values codified from MCP-3066 (the shell backstop is the source of
// truth). Documented with rationale in reviewer-liveness.example.json.
const (
	wantSLAMinutes          = 120 // T: 2h silence on the current head => silent stall (mode1).
	wantMaxFallbacksPerHead = 1   // N: one fallback hop per head SHA (resets when the head moves).
	wantMaxHops             = 2   // <=2 cumulative hops, then escalate to the CEO.
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read %s: %v", schemaFile, err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaFile, doc); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	sch, err := c.Compile(schemaFile)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func mustInstance(t *testing.T, jsonStr string) any {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(jsonStr))
	if err != nil {
		t.Fatalf("parse instance: %v", err)
	}
	return inst
}

// TestSchema_BackCompat: policies with no liveness block (every policy that
// existed before this change) MUST still validate. The schema also ships
// `examples`; all of them must validate.
func TestSchema_BackCompat(t *testing.T) {
	sch := compileSchema(t)

	legacy := []string{
		// Design gate: review (Critic) then user approval, no liveness.
		`{"mode":"normal","stages":[
			{"type":"review","label":"Adversarial review","participants":[{"type":"agent","agentId":"a"}]},
			{"type":"approval","label":"Per-spec design sign-off","participants":[{"type":"user","userId":"local-board"}]}
		]}`,
		// Pre-merge gate only.
		`{"mode":"normal","stages":[
			{"type":"approval","label":"Pre-merge","participants":[{"type":"user","userId":"local-board"}]}
		]}`,
	}
	for i, p := range legacy {
		if err := sch.Validate(mustInstance(t, p)); err != nil {
			t.Errorf("legacy policy %d must validate (back-compat): %v", i, err)
		}
	}

	// Every example embedded in the schema must validate against the schema.
	raw, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schemaDoc struct {
		Examples []json.RawMessage `json:"examples"`
	}
	if err := json.Unmarshal(raw, &schemaDoc); err != nil {
		t.Fatalf("decode schema examples: %v", err)
	}
	if len(schemaDoc.Examples) == 0 {
		t.Fatal("schema must ship at least one example")
	}
	for i, ex := range schemaDoc.Examples {
		if err := sch.Validate(mustInstance(t, string(ex))); err != nil {
			t.Errorf("schema example %d does not validate: %v", i, err)
		}
	}
}

// TestSchema_LivenessRosterAccepted: a review stage carrying a full
// Reviewer-Liveness Contract validates.
func TestSchema_LivenessRosterAccepted(t *testing.T) {
	sch := compileSchema(t)
	valid := `{"mode":"normal","stages":[
		{"type":"review","label":"Adversarial review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
		 "liveness":{
			"slaMinutes":120,
			"maxFallbacksPerHead":1,
			"maxHops":2,
			"escalateTo":{"type":"agent","agentId":"ceo"},
			"failoverStallModes":["silent","stale_ci_pending"],
			"roster":[
				{"agentId":"kimi","name":"KimiReviewer","model":"kimi-k2","modelFamily":"moonshot"},
				{"agentId":"gemini","name":"GeminiCritic","model":"gemini-2.5","modelFamily":"gemini"},
				{"agentId":"glm","name":"GLMReviewer","model":"glm-4.7","modelFamily":"glm"}
			]
		 }},
		{"type":"approval","label":"Per-spec design sign-off","participants":[{"type":"user","userId":"local-board"}]}
	]}`
	if err := sch.Validate(mustInstance(t, valid)); err != nil {
		t.Fatalf("valid liveness policy must validate: %v", err)
	}
}

// TestSchema_LivenessRejectsMalformed: the schema must reject malformed liveness
// blocks. Before the contract existed these passed silently (additionalProperties
// was open), so this is the red->green guard for the schema change.
func TestSchema_LivenessRejectsMalformed(t *testing.T) {
	sch := compileSchema(t)
	bad := map[string]string{
		"roster entry missing modelFamily": `{"mode":"normal","stages":[
			{"type":"review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
			 "liveness":{"roster":[{"agentId":"kimi","name":"KimiReviewer"}]}}
		]}`,
		"empty roster": `{"mode":"normal","stages":[
			{"type":"review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
			 "liveness":{"roster":[]}}
		]}`,
		"liveness without roster": `{"mode":"normal","stages":[
			{"type":"review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
			 "liveness":{"slaMinutes":120}}
		]}`,
		"unknown stall mode": `{"mode":"normal","stages":[
			{"type":"review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
			 "liveness":{"failoverStallModes":["substantive"],"roster":[{"agentId":"kimi","modelFamily":"moonshot"}]}}
		]}`,
		"negative slaMinutes": `{"mode":"normal","stages":[
			{"type":"review","participants":[{"type":"agent","agentId":"codex","modelFamily":"openai"}],
			 "liveness":{"slaMinutes":0,"roster":[{"agentId":"kimi","modelFamily":"moonshot"}]}}
		]}`,
	}
	for name, p := range bad {
		if err := sch.Validate(mustInstance(t, p)); err == nil {
			t.Errorf("%s: schema must reject this policy but it validated", name)
		}
	}
}

// reviewerLiveness mirrors the contract block for the cross-entity invariant
// checks that JSON Schema cannot express.
type participant struct {
	Type        string `json:"type"`
	UserID      string `json:"userId"`
	AgentID     string `json:"agentId"`
	ModelFamily string `json:"modelFamily"`
}

type rosterEntry struct {
	AgentID     string `json:"agentId"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	ModelFamily string `json:"modelFamily"`
}

type reviewerLiveness struct {
	SLAMinutes          int           `json:"slaMinutes"`
	MaxFallbacksPerHead int           `json:"maxFallbacksPerHead"`
	MaxHops             int           `json:"maxHops"`
	EscalateTo          participant   `json:"escalateTo"`
	FailoverStallModes  []string      `json:"failoverStallModes"`
	Roster              []rosterEntry `json:"roster"`
}

type stage struct {
	Type         string            `json:"type"`
	Participants []participant     `json:"participants"`
	Liveness     *reviewerLiveness `json:"liveness"`
}

type policy struct {
	Mode   string  `json:"mode"`
	Stages []stage `json:"stages"`
}

// TestExampleFile_SchemaAndContractValues: the committed canonical example
// validates against the schema, carries the MCP-3066 contract values, and obeys
// the same-family exclusion invariant (no roster reviewer shares the primary
// reviewer's modelFamily — the shell backstop drops the gpt-5.5 Critic because
// the primary CodexReviewer is also gpt-5.5).
func TestExampleFile_SchemaAndContractValues(t *testing.T) {
	sch := compileSchema(t)

	raw, err := os.ReadFile(exampleFile)
	if err != nil {
		t.Fatalf("read %s: %v", exampleFile, err)
	}
	if err := sch.Validate(mustInstance(t, string(raw))); err != nil {
		t.Fatalf("%s fails schema contract: %v", exampleFile, err)
	}

	var p policy
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode %s: %v", exampleFile, err)
	}

	var live *reviewerLiveness
	var primary participant
	for _, s := range p.Stages {
		if s.Liveness != nil {
			live = s.Liveness
			if len(s.Participants) > 0 {
				primary = s.Participants[0]
			}
			break
		}
	}
	if live == nil {
		t.Fatal("example must contain a review stage with a liveness block")
	}

	if live.SLAMinutes != wantSLAMinutes {
		t.Errorf("slaMinutes = %d, want %d (MCP-3066 SLA T=2h)", live.SLAMinutes, wantSLAMinutes)
	}
	if live.MaxFallbacksPerHead != wantMaxFallbacksPerHead {
		t.Errorf("maxFallbacksPerHead = %d, want %d (<=1 fallback/head)", live.MaxFallbacksPerHead, wantMaxFallbacksPerHead)
	}
	if live.MaxHops != wantMaxHops {
		t.Errorf("maxHops = %d, want %d (<=2 hops then escalate to CEO)", live.MaxHops, wantMaxHops)
	}
	if len(live.Roster) == 0 {
		t.Fatal("roster must be non-empty")
	}

	// Same-family exclusion (FR-011 model diversity): no roster reviewer may
	// share the primary reviewer's model family, and roster families must be
	// distinct from one another.
	if primary.ModelFamily == "" {
		t.Fatal("primary reviewer must declare a modelFamily so the same-family exclusion is checkable")
	}
	seen := map[string]bool{primary.ModelFamily: true}
	for _, r := range live.Roster {
		if r.ModelFamily == "" {
			t.Errorf("roster entry %q missing modelFamily", r.AgentID)
			continue
		}
		if r.ModelFamily == primary.ModelFamily {
			t.Errorf("roster reviewer %q shares the primary reviewer's family %q (same-family exclusion violated)", r.AgentID, primary.ModelFamily)
		}
		if seen[r.ModelFamily] {
			t.Errorf("roster reviewer %q duplicates family %q (roster must be model-diverse)", r.AgentID, r.ModelFamily)
		}
		seen[r.ModelFamily] = true
	}

	// substantive request_changes is a mandatory fence: it must never be a
	// failover-eligible stall mode (mode3 in the shell backstop).
	for _, m := range live.FailoverStallModes {
		if m == "substantive" {
			t.Error("failoverStallModes must not include 'substantive' — a substantive request_changes is a mandatory fence (mode3)")
		}
	}
}
