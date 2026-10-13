package replaycorpus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func marshalLine(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// secretBody is a string that exists nowhere else, so finding it anywhere in
// the loaded corpus proves content escaped the loader.
const secretBody = "ZZ-CANARY-BODY-TEXT-must-not-escape-ZZ"

func bodiesOnRecord(t *testing.T, fields map[string]any) string {
	t.Helper()
	base := map[string]any{
		"id":              "b-1",
		"type":            "tool_call",
		"server_name":     "github",
		"tool_name":       "list_issues",
		"status":          "success",
		"work_session_id": "ws-1",
		"request_id":      "req-1",
		"timestamp":       "2026-08-01T10:00:00Z",
		"response":        secretBody + " one two three",
		"arguments":       map[string]any{"query": secretBody},
		"response_bytes":  60,
		"request_bytes":   40,
	}
	for k, v := range fields {
		base[k] = v
	}
	return marshalLine(t, base)
}

// assertNoText walks EVERY string reachable from v — exported or not, through
// pointers, slices, maps and structs — looking for needle. json.Marshal alone
// would miss an unexported field, which is exactly where a body would hide.
func assertNoText(t *testing.T, v any, needle string) {
	t.Helper()
	seen := map[uintptr]bool{}
	var walk func(rv reflect.Value, path string)
	walk = func(rv reflect.Value, path string) {
		switch rv.Kind() {
		case reflect.String:
			if strings.Contains(rv.String(), needle) {
				t.Errorf("body text escaped the loader at %s: %q", path, rv.String())
			}
		case reflect.Ptr, reflect.Interface:
			if rv.IsNil() {
				return
			}
			if rv.Kind() == reflect.Ptr {
				if seen[rv.Pointer()] {
					return
				}
				seen[rv.Pointer()] = true
			}
			walk(rv.Elem(), path)
		case reflect.Slice, reflect.Array:
			for i := 0; i < rv.Len(); i++ {
				walk(rv.Index(i), path+"[]")
			}
		case reflect.Map:
			for _, k := range rv.MapKeys() {
				walk(k, path+".key")
				walk(rv.MapIndex(k), path+"[k]")
			}
		case reflect.Struct:
			for i := 0; i < rv.NumField(); i++ {
				walk(rv.Field(i), path+"."+rv.Type().Field(i).Name)
			}
		default:
		}
	}
	walk(reflect.ValueOf(v), "corpus")
}

func TestBodiesOffIsTheDefault(t *testing.T) {
	// The zero value of Options must be the safe one: an operator who forgets
	// to think about the flag gets bodies-off, not an unmasked raw load.
	if (Options{}).Bodies != BodiesOff {
		t.Fatal("the zero value of Options is not bodies-off")
	}
	opts := testOptions(t.TempDir())
	if opts.Bodies != BodiesOff {
		t.Fatal("test options are not bodies-off")
	}
	c := loadString(t, bodiesOnRecord(t, nil), opts)
	if c.Bodies != BodiesOff {
		t.Errorf("Corpus.Bodies = %s, want %s", c.Bodies, BodiesOff)
	}
	assertNoText(t, c, secretBody)
	if len(c.Warnings) != 0 {
		t.Errorf("a bodies-off load warned unnecessarily: %v", c.Warnings)
	}
}

func TestBodiesOnRequiresExplicitOptInAndWarns(t *testing.T) {
	var warnings []string
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked
	opts.Warnf = func(format string, args ...any) {
		warnings = append(warnings, format)
	}
	c := loadString(t, bodiesOnRecord(t, nil), opts)

	if len(warnings) == 0 {
		t.Fatal("bodies-on printed no warning")
	}
	joined := strings.ToLower(strings.Join(warnings, " ") + " " + strings.Join(c.Warnings, " "))
	// The warning has to say the thing that makes bodies-on dangerous: the
	// export path does not mask, so this is raw traffic.
	for _, want := range []string{"mask", "unmask"} {
		if strings.Contains(joined, want) {
			return
		}
	}
	t.Errorf("bodies-on warning does not say the export is unmasked: %v", warnings)
}

func TestNoBodyTextEscapesOnABodiesOnLoad(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked
	c := loadString(t, bodiesOnRecord(t, nil), opts)
	assertNoText(t, c, secretBody)
}

func TestTokenizationHappensInsideTheLoaderAndOnlyCountsCross(t *testing.T) {
	var seen []string
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked
	opts.counter = countingStub{seen: &seen}

	c := loadString(t, bodiesOnRecord(t, nil), opts)
	call := firstCall(t, c)

	if call.ResponseCost.Basis != CostMeasured {
		t.Fatalf("ResponseCost.Basis = %s, want %s", call.ResponseCost.Basis, CostMeasured)
	}
	if call.ResponseCost.Tokens != 4 {
		t.Errorf("ResponseCost.Tokens = %d, want 4", call.ResponseCost.Tokens)
	}
	if call.RequestCost.Basis != CostMeasured {
		t.Errorf("RequestCost.Basis = %s, want %s", call.RequestCost.Basis, CostMeasured)
	}
	// The body reached the tokenizer, and the tokenizer is inside this package.
	if len(seen) == 0 || !strings.Contains(strings.Join(seen, " "), secretBody) {
		t.Fatalf("the body never reached the in-package tokenizer: %v", seen)
	}
	// And nothing but counts came back out.
	assertNoText(t, c, secretBody)
}

// The log stores the FULL pre-truncation retrieve_tools response while the
// agent consumed the cut text. Tokenizing it as-is OVERSTATES what mcpproxy
// cost — the one direction of error this benchmark exists to prevent.
func TestTruncatedRetrieveToolsIsNeverTokenized(t *testing.T) {
	var seen []string
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked
	opts.counter = countingStub{seen: &seen}

	c := loadString(t, bodiesOnRecord(t, map[string]any{
		"type":      "internal_tool_call",
		"tool_name": "retrieve_tools",
		// Arguments carry no canary: they are recorded whole and are legitimately
		// tokenized. It is the RESPONSE that must never be counted here.
		"arguments":          map[string]any{"query": "plain"},
		"server_name":        "",
		"response_truncated": true,
		"response_bytes":     9999,
	}), opts)

	call := firstCall(t, c)
	if call.ResponseCost.Basis != CostUnavailable {
		t.Fatalf("Basis = %s, want %s (tokenizing would overstate)", call.ResponseCost.Basis, CostUnavailable)
	}
	if call.ResponseCost.Reason != ReasonTruncatedRetrieveOverstates {
		t.Errorf("Reason = %s, want %s", call.ResponseCost.Reason, ReasonTruncatedRetrieveOverstates)
	}
	if c.Exclusions.Withheld[ReasonTruncatedRetrieveOverstates] != 1 {
		t.Errorf("the exclusion was not counted: %+v", c.Exclusions.Withheld)
	}
	// The pre-truncation byte length describes the stored text, not what the
	// agent paid, so it must not be used as an estimate either.
	if call.ResponseCost.Tokens != 0 || call.ResponseCost.Bytes != 0 {
		t.Errorf("an excluded cost still carries figures: %+v", call.ResponseCost)
	}
	if strings.Contains(strings.Join(seen, " "), secretBody) {
		t.Error("the truncated retrieve_tools response was tokenized anyway")
	}
}

func TestSensitiveBodyIsNeverTokenized(t *testing.T) {
	var seen []string
	opts := testOptions(t.TempDir())
	opts.Bodies = BodiesOnUnmasked
	opts.counter = countingStub{seen: &seen}

	c := loadString(t, bodiesOnRecord(t, map[string]any{"has_sensitive_data": true}), opts)
	call := firstCall(t, c)
	if call.ResponseCost.Basis == CostMeasured {
		t.Error("a flagged-sensitive body was tokenized")
	}
	if strings.Contains(strings.Join(seen, " "), secretBody) {
		t.Error("a flagged-sensitive body reached the tokenizer")
	}
	if c.Exclusions.Withheld[ReasonSensitive] == 0 {
		t.Errorf("the sensitive withholding was not counted: %+v", c.Exclusions.Withheld)
	}
}

func TestLoadFileRefusesAPathInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git"), "gitdir: elsewhere\n")
	inside := filepath.Join(root, "bench", "input.jsonl")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately NOT created: the refusal must precede any file I/O, so a
	// path that does not exist still fails with the privacy error.
	_, err := LoadFile(inside, testOptions(root))
	if !errors.Is(err, ErrInsideRepository) {
		t.Fatalf("err = %v, want ErrInsideRepository", err)
	}
	if !strings.Contains(err.Error(), "never committed") {
		t.Errorf("error %q does not explain why", err)
	}
}

func TestLoadFileRefusesTheRealRepositoryWorkingTree(t *testing.T) {
	// No repoRoot override: this exercises the real detection, from the test
	// binary's working directory (bench/replaycorpus) up to the checkout.
	opts := Options{Warnf: func(string, ...any) {}, counter: countingStub{}}
	_, err := LoadFile("activity.jsonl", opts)
	if !errors.Is(err, ErrInsideRepository) {
		t.Fatalf("err = %v, want ErrInsideRepository", err)
	}
}

func TestLoadFileAcceptsAPathOutsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	outside := filepath.Join(t.TempDir(), "activity.jsonl")
	writeFile(t, outside, jsonlToolCall+"\n")

	c, err := LoadFile(outside, testOptions(repo))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.RecordsRead != 1 {
		t.Errorf("RecordsRead = %d, want 1", c.RecordsRead)
	}
}
