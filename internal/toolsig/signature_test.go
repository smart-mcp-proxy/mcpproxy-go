package toolsig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestRender_GrammarWorkedExamples pins the E1–E11 worked examples from
// contracts/signature-grammar.md byte-for-byte (FR-002/003/004/019, SC-004).
func TestRender_GrammarWorkedExamples(t *testing.T) {
	tests := []struct {
		name        string
		paramsJSON  string
		description string
		wantSig     string
		wantDesc    string
		wantLossy   bool
	}{
		{
			name:        "E1 no-params tool: empty parens, never lossy",
			paramsJSON:  `{"type":"object","properties":{}}`,
			description: "List all configured servers.",
			wantSig:     "()",
			wantDesc:    "List all configured servers.",
			wantLossy:   false,
		},
		{
			name: "E2 all-scalar: required first, optionals sorted, short default inline",
			paramsJSON: `{"type":"object",
				"properties":{"origin":{"type":"string"},"ttl":{"type":"integer","default":3600},
				              "certificate_id":{"type":"string"},"custom_domain":{"type":"string"}},
				"required":["origin"]}`,
			wantSig:   "(origin*:str, certificate_id:str, custom_domain:str, ttl:int=3600)",
			wantLossy: false,
		},
		{
			name: "E3 nested REQUIRED param: never-elide + lossy collapse, marker order *~",
			paramsJSON: `{"type":"object",
				"properties":{"name":{"type":"string"},
				              "account":{"type":"object","properties":{"id":{"type":"string"},"tier":{"type":"string"}}}},
				"required":["name","account"]}`,
			wantSig:   "(name*:str, account*~:obj)",
			wantLossy: true,
		},
		{
			name:       "E4 short enum (<=5) inlined",
			paramsJSON: `{"type":"object","properties":{"type":{"enum":["full","partial"]}},"required":["type"]}`,
			wantSig:    "(type*:enum[full|partial])",
			wantLossy:  false,
		},
		{
			name: "E4 long enum (>5) collapses to base type + lossy",
			paramsJSON: `{"type":"object",
				"properties":{"region":{"type":"string",
					"enum":["nyc1","nyc3","sfo3","ams3","sgp1","lon1","fra1","tor1","blr1"]}},
				"required":["region"]}`,
			wantSig:   "(region*~:str)",
			wantLossy: true,
		},
		{
			name:        "E5 empty description: sig only, empty desc",
			paramsJSON:  `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			description: "",
			wantSig:     "(path*:str)",
			wantDesc:    "",
			wantLossy:   false,
		},
		{
			name:        "E6 CJK description: unconditional terminator",
			paramsJSON:  `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`,
			description: "検索クエリを実行します。結果はJSONで返されます。",
			wantSig:     "(q*:str)",
			wantDesc:    "検索クエリを実行します。",
			wantLossy:   false,
		},
		{
			name: "E7 array of string non-lossy, array of object lossy, optionals sorted",
			paramsJSON: `{"type":"object",
				"properties":{"tags":{"type":"array","items":{"type":"string"}},
				              "filters":{"type":"array","items":{"type":"object","properties":{"k":{"type":"string"}}}}}}`,
			wantSig:   "(filters~:[obj], tags:[str])",
			wantLossy: true,
		},
		{
			name:       "E8 required name absent from properties: never dropped, any + lossy",
			paramsJSON: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path","token"]}`,
			wantSig:    "(path*:str, token*~:any)",
			wantLossy:  true,
		},
		{
			name:       "E9 metachar in name and enum value: quoted atoms",
			paramsJSON: `{"type":"object","properties":{"filter:key":{"enum":["a|b","c"]}},"required":["filter:key"]}`,
			wantSig:    `("filter:key"*:enum["a|b"|c])`,
			wantLossy:  false,
		},
		{
			name:       "E10 non-null type union in declared order",
			paramsJSON: `{"type":"object","properties":{"id":{"type":["string","integer"]}},"required":["id"]}`,
			wantSig:    "(id*:str|int)",
			wantLossy:  false,
		},
		{
			name:       "E10 nullable union drops null member",
			paramsJSON: `{"type":"object","properties":{"note":{"type":["string","null"]}}}`,
			wantSig:    "(note:str)",
			wantLossy:  false,
		},
		{
			name:       "E11 whole schema unparseable: (~) fallback, lossy, never ()",
			paramsJSON: `not valid json {`,
			wantSig:    "(~)",
			wantLossy:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, err := Render(tt.paramsJSON, tt.description)
			if err != nil && tt.wantSig != "(~)" {
				t.Fatalf("Render() unexpected error: %v", err)
			}
			if sig.Sig != tt.wantSig {
				t.Errorf("Sig = %q, want %q", sig.Sig, tt.wantSig)
			}
			if sig.Desc != tt.wantDesc {
				t.Errorf("Desc = %q, want %q", sig.Desc, tt.wantDesc)
			}
			if sig.Lossy != tt.wantLossy {
				t.Errorf("Lossy = %v, want %v", sig.Lossy, tt.wantLossy)
			}
			// Lossy-legibility biconditional: Lossy == contains(sig, "~").
			if got := strings.Contains(sig.Sig, "~"); got != sig.Lossy {
				t.Errorf("invariant violated: Lossy=%v but contains(sig,\"~\")=%v (sig=%q)", sig.Lossy, got, sig.Sig)
			}
		})
	}
}

// TestRender_EmptyParamsJSON: an empty stored schema means "no declared
// params" (mirrors the tolerant inputSchema fallback in the full-mode path) —
// not an unparseable schema.
func TestRender_EmptyParamsJSON(t *testing.T) {
	sig, err := Render("", "Do a thing.")
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if sig.Sig != "()" || sig.Lossy {
		t.Errorf("empty ParamsJSON: got sig=%q lossy=%v, want () false", sig.Sig, sig.Lossy)
	}
}

// TestRender_NonObjectSchema: valid JSON that is not an object schema is
// unparseable-as-a-schema — the (~) fallback applies.
func TestRender_NonObjectSchema(t *testing.T) {
	for _, paramsJSON := range []string{`[1,2,3]`, `"a string"`, `42`} {
		sig, _ := Render(paramsJSON, "")
		if sig.Sig != "(~)" || !sig.Lossy {
			t.Errorf("Render(%q): got sig=%q lossy=%v, want (~) true", paramsJSON, sig.Sig, sig.Lossy)
		}
	}
}

// TestRender_Deterministic: identical definition in, identical bytes out
// (FR-019) — including under shuffled property insertion order, which in Go
// means constructing the JSON with the same properties serialized in
// different key orders.
func TestRender_Deterministic(t *testing.T) {
	// Build the same schema with properties in two different declared orders.
	mkSchema := func(order []string) string {
		props := map[string]string{
			"origin":         `{"type":"string"}`,
			"ttl":            `{"type":"integer","default":3600}`,
			"certificate_id": `{"type":"string"}`,
			"custom_domain":  `{"type":"string"}`,
			"account":        `{"type":"object","properties":{"id":{"type":"string"}}}`,
		}
		var b strings.Builder
		b.WriteString(`{"type":"object","properties":{`)
		for i, name := range order {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "%q:%s", name, props[name])
		}
		b.WriteString(`},"required":["origin","account"]}`)
		return b.String()
	}

	orderA := []string{"origin", "ttl", "certificate_id", "custom_domain", "account"}
	orderB := []string{"account", "custom_domain", "certificate_id", "ttl", "origin"}

	sigA, _ := Render(mkSchema(orderA), "Create a CDN.")
	sigB, _ := Render(mkSchema(orderB), "Create a CDN.")
	if sigA != sigB {
		t.Errorf("shuffled property insertion order changed output:\nA: %+v\nB: %+v", sigA, sigB)
	}

	// Repeated renders are byte-identical.
	for i := 0; i < 10; i++ {
		sigN, _ := Render(mkSchema(orderA), "Create a CDN.")
		if sigN != sigA {
			t.Fatalf("run %d differs: %+v vs %+v", i, sigN, sigA)
		}
	}
}

// TestRender_LossyBiconditional_Synthetic sweeps schema shapes and asserts the
// single source of truth for the lossy invariant: Lossy == contains(sig,"~").
func TestRender_LossyBiconditional_Synthetic(t *testing.T) {
	schemas := []string{
		`{"type":"object","properties":{}}`,
		`{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
		`{"type":"object","properties":{"a":{"type":"object"}}}`,
		`{"type":"object","properties":{"a":{"$ref":"#/defs/x"}},"required":["a"]}`,
		`{"type":"object","properties":{"a":{"type":"array"}}}`,
		`{"type":"object","properties":{"a":{"oneOf":[{"type":"object"},{"type":"object"}]}}}`,
		`{"type":"object","required":["ghost"]}`,
		`broken {`,
		``,
	}
	for _, s := range schemas {
		sig, _ := Render(s, "")
		if got := strings.Contains(sig.Sig, "~"); got != sig.Lossy {
			t.Errorf("schema %q: Lossy=%v, contains(sig,~)=%v, sig=%q", s, sig.Lossy, got, sig.Sig)
		}
	}
}

// TestRender_RequiredOrderAndDedup: required params render in the schema
// required-array order, verbatim, de-duplicated.
func TestRender_RequiredOrderAndDedup(t *testing.T) {
	paramsJSON := `{"type":"object",
		"properties":{"b":{"type":"string"},"a":{"type":"integer"},"c":{"type":"boolean"}},
		"required":["c","a","c"]}`
	sig, _ := Render(paramsJSON, "")
	want := "(c*:bool, a*:int, b:str)"
	if sig.Sig != want {
		t.Errorf("Sig = %q, want %q", sig.Sig, want)
	}
}

// TestRender_DefaultRules: defaults render only for optional scalars with a
// short literal; required params never carry defaults; long/composite
// defaults are dropped.
func TestRender_DefaultRules(t *testing.T) {
	tests := []struct {
		name       string
		paramsJSON string
		wantSig    string
	}{
		{
			name:       "required param never carries default",
			paramsJSON: `{"type":"object","properties":{"mode":{"type":"string","default":"auto"}},"required":["mode"]}`,
			wantSig:    "(mode*:str)",
		},
		{
			name:       "optional bool default",
			paramsJSON: `{"type":"object","properties":{"dry_run":{"type":"boolean","default":false}}}`,
			wantSig:    "(dry_run:bool=false)",
		},
		{
			name:       "default longer than 20 chars dropped",
			paramsJSON: `{"type":"object","properties":{"tpl":{"type":"string","default":"aaaaaaaaaaaaaaaaaaaaaaaaa"}}}`,
			wantSig:    "(tpl:str)",
		},
		{
			name:       "metachar default quoted",
			paramsJSON: `{"type":"object","properties":{"sep":{"type":"string","default":"=|"}}}`,
			wantSig:    `(sep:str="=|")`,
		},
		{
			name:       "composite default dropped (array)",
			paramsJSON: `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"},"default":["a"]}}}`,
			wantSig:    "(tags:[str])",
		},
		{
			name:       "null default renders as null",
			paramsJSON: `{"type":"object","properties":{"cursor":{"type":"string","default":null}}}`,
			wantSig:    "(cursor:str=null)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, _ := Render(tt.paramsJSON, "")
			if sig.Sig != tt.wantSig {
				t.Errorf("Sig = %q, want %q", sig.Sig, tt.wantSig)
			}
		})
	}
}

// TestRender_AtomEscaping_RoundTrip: every metacharacter forces quoting and a
// reference parser recovers the original atom (§3.5 unambiguous/reversible).
func TestRender_AtomEscaping_RoundTrip(t *testing.T) {
	metachars := []string{" ", ",", ":", "|", "=", "(", ")", "*", "~", "[", "]", `"`}
	for _, mc := range metachars {
		name := "k" + mc + "v"
		schema := map[string]any{
			"type":       "object",
			"properties": map[string]any{name: map[string]any{"type": "string"}},
			"required":   []any{name},
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			t.Fatal(err)
		}
		sig, _ := Render(string(raw), "")
		quoted := quoteAtom(name)
		wantSig := "(" + quoted + "*:str)"
		if sig.Sig != wantSig {
			t.Errorf("metachar %q: Sig = %q, want %q", mc, sig.Sig, wantSig)
		}
		if !strings.HasPrefix(quoted, `"`) {
			t.Errorf("metachar %q: atom must be quoted, got %q", mc, quoted)
		}
		got, err := parseQuotedAtom(quoted)
		if err != nil || got != name {
			t.Errorf("metachar %q: round-trip got %q (err=%v), want %q", mc, got, err, name)
		}
	}

	// Empty string quotes too.
	if quoteAtom("") != `""` {
		t.Errorf("empty atom must render as %q, got %q", `""`, quoteAtom(""))
	}
	// Identifier-like atoms stay bare.
	for _, bare := range []string{"origin", "3600", "auto", "full", "a\\b"} {
		if quoteAtom(bare) != bare {
			t.Errorf("atom %q must stay bare, got %q", bare, quoteAtom(bare))
		}
	}
	// Embedded quote and backslash escape correctly and round-trip.
	for _, tricky := range []string{`a"b`, `a"\"b`, `end"`} {
		q := quoteAtom(tricky)
		got, err := parseQuotedAtom(q)
		if err != nil || got != tricky {
			t.Errorf("tricky atom %q: quoted=%q round-trip=%q err=%v", tricky, q, got, err)
		}
	}
}

// TestRender_TildeAtoms_LossyBiconditional: a literal "~" inside an atom
// (name / enum value / default) must never leak into the signature string —
// otherwise a non-lossy signature would contain "~" and break the strict
// biconditional Lossy ⟺ contains(sig,"~") (grammar contract §1/§3.5). Quoted
// atoms therefore escape "~" to a tilde-free sequence (~).
func TestRender_TildeAtoms_LossyBiconditional(t *testing.T) {
	tests := []struct {
		name       string
		paramsJSON string
		wantLossy  bool
	}{
		{
			name:       "tilde in property name",
			paramsJSON: `{"type":"object","properties":{"k~v":{"type":"string"}},"required":["k~v"]}`,
			wantLossy:  false,
		},
		{
			name:       "tilde in enum value",
			paramsJSON: `{"type":"object","properties":{"mode":{"enum":["a~b","c"]}},"required":["mode"]}`,
			wantLossy:  false,
		},
		{
			name:       "tilde in default literal",
			paramsJSON: `{"type":"object","properties":{"sep":{"type":"string","default":"~"}}}`,
			wantLossy:  false,
		},
		{
			name:       "tilde atom on a genuinely lossy param keeps exactly the marker tilde",
			paramsJSON: `{"type":"object","properties":{"k~v":{"type":"object"}},"required":["k~v"]}`,
			wantLossy:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, err := Render(tt.paramsJSON, "")
			if err != nil {
				t.Fatalf("Render() unexpected error: %v", err)
			}
			if sig.Lossy != tt.wantLossy {
				t.Errorf("Lossy = %v, want %v (sig=%q)", sig.Lossy, tt.wantLossy, sig.Sig)
			}
			if got := strings.Contains(sig.Sig, "~"); got != sig.Lossy {
				t.Errorf("biconditional violated: Lossy=%v but contains(sig,\"~\")=%v (sig=%q)",
					sig.Lossy, got, sig.Sig)
			}
		})
	}

	// The escape stays reversible: quoteAtom/parseQuotedAtom round-trip a
	// tilde-bearing atom, and the quoted form is tilde-free.
	for _, atom := range []string{"k~v", "~", "~~", `a"~\b`} {
		q := quoteAtom(atom)
		if strings.Contains(q, "~") {
			t.Errorf("quoteAtom(%q) = %q still contains a literal ~", atom, q)
		}
		got, err := parseQuotedAtom(q)
		if err != nil || got != atom {
			t.Errorf("round-trip of %q via %q got %q (err=%v)", atom, q, got, err)
		}
	}
}

// TestRender_NumericEnumValues: numeric enum values render deterministically.
func TestRender_NumericEnumValues(t *testing.T) {
	sig, _ := Render(`{"type":"object","properties":{"level":{"enum":[1,2,3]}},"required":["level"]}`, "")
	want := "(level*:enum[1|2|3])"
	if sig.Sig != want {
		t.Errorf("Sig = %q, want %q", sig.Sig, want)
	}
}

// TestRender_AnyOfNullable: anyOf [{string},{null}] resolves to str (§3).
func TestRender_AnyOfNullable(t *testing.T) {
	sig, _ := Render(`{"type":"object","properties":{"note":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`, "")
	want := "(note:str)"
	if sig.Sig != want || sig.Lossy {
		t.Errorf("got sig=%q lossy=%v, want %q false", sig.Sig, sig.Lossy, want)
	}
}
