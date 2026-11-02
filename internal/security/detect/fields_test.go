package detect

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func fieldPaths(fs []Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path + "=" + f.Text
	}
	return out
}

func TestFieldStrings_DescriptionOnly(t *testing.T) {
	fs, partial := FieldStrings(ToolView{Description: "Adds numbers."})
	if partial != "" {
		t.Fatal("partial = true for a plain description")
	}
	if len(fs) != 1 || fs[0].Path != "description" || !fs[0].Exact || fs[0].RawEnd != len("Adds numbers.") {
		t.Fatalf("fields = %+v", fs)
	}
}

func TestFieldStrings_NestedSchemaValuesInDocumentOrder(t *testing.T) {
	schema := `{"type":"object","properties":{"query":{"type":"string","description":"Search text"},"tags":{"type":"array","items":{"type":"string","enum":["a","b"]}},"a/b~c":{"description":"odd key"}},"$defs":{"X":{"description":"def"}},"required":["query"]}`
	fs, partial := FieldStrings(ToolView{Description: "d", InputSchema: json.RawMessage(schema), OutputSchema: json.RawMessage(`{"description":"out"}`)})
	if partial != "" {
		t.Fatal("partial = true")
	}
	want := []string{
		"description=d",
		"input_schema:/type=object",
		"input_schema:/properties/query/type=string",
		"input_schema:/properties/query/description=Search text",
		"input_schema:/properties/tags/type=array",
		"input_schema:/properties/tags/items/type=string",
		"input_schema:/properties/tags/items/enum/0=a",
		"input_schema:/properties/tags/items/enum/1=b",
		"input_schema:/properties/a~1b~0c/description=odd key",
		"input_schema:/$defs/X/description=def",
		"input_schema:/required/0=query",
		"output_schema:/description=out",
	}
	if got := fieldPaths(fs); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths:\n got %q\nwant %q", got, want)
	}
	// Exact raw offsets for an escape-free token.
	for _, f := range fs {
		if f.Path == "input_schema:/properties/query/description" {
			if !f.Exact || schema[f.RawStart:f.RawEnd] != `"Search text"` {
				t.Fatalf("raw token = %q exact=%v", schema[f.RawStart:f.RawEnd], f.Exact)
			}
		}
	}
}

func TestFieldStrings_EscapesAreDecodedButInexact(t *testing.T) {
	schema := `{"description":"line one\nline \"two\" é"}`
	fs, _ := FieldStrings(ToolView{InputSchema: json.RawMessage(schema)})
	if len(fs) != 2 {
		t.Fatalf("fields = %+v", fs)
	}
	f := fs[1]
	if f.Text != "line one\nline \"two\" é" || f.Exact {
		t.Fatalf("text=%q exact=%v", f.Text, f.Exact)
	}
	if sp := FieldSpan("x", TierSoft, ToolView{InputSchema: json.RawMessage(schema)}, f, 0, 4); sp != nil {
		t.Fatalf("inexact field must yield no span, got %+v", sp)
	}
}

func TestFieldSpan_ExactSchemaString(t *testing.T) {
	schema := `{"a":"zz","description":"héllo world"}`
	tool := ToolView{InputSchema: json.RawMessage(schema)}
	fs, _ := FieldStrings(tool)
	f := fs[2]
	if f.Text != "héllo world" {
		t.Fatalf("text=%q", f.Text)
	}
	start := strings.Index(f.Text, "world")
	sp := FieldSpan("chk", TierSoft, tool, f, start, start+len("world"))
	if len(sp) != 1 {
		t.Fatalf("span = %+v", sp)
	}
	// UTF-16 offsets against the RAW schema: the raw prefix up to "world" is
	// `{"a":"zz","description":"héllo ` = 25 code units (é is one unit).
	wantStart := len([]rune(`{"a":"zz","description":"héllo `))
	if sp[0].Field != SpanFieldInputSchema || sp[0].Start != wantStart || sp[0].End != wantStart+5 || sp[0].Snippet != "world" {
		t.Fatalf("span = %+v (want start %d)", sp[0], wantStart)
	}
}

func TestFieldStrings_MalformedSchemaYieldsPrefix(t *testing.T) {
	fs, partial := FieldStrings(ToolView{InputSchema: json.RawMessage(`{"description":"ok","x":@bad}`)})
	if partial == "" || len(fs) != 2 || fs[1].Text != "ok" {
		t.Fatalf("fields=%+v partial=%v", fs, partial)
	}
}

func TestFieldStrings_Caps(t *testing.T) {
	// depth
	deep := strings.Repeat(`{"a":`, MaxFieldDepth+1) + `"v"` + strings.Repeat("}", MaxFieldDepth+1)
	if _, partial := FieldStrings(ToolView{InputSchema: json.RawMessage(deep)}); partial == "" {
		t.Fatal("depth cap not reported")
	}
	// count
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < MaxFieldsPerSchema+5; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"s"`)
	}
	b.WriteString("]")
	fs, partial := FieldStrings(ToolView{InputSchema: json.RawMessage(b.String())})
	if partial == "" || len(fs) != MaxFieldsPerSchema+1 {
		t.Fatalf("count cap: partial=%v len=%d", partial, len(fs))
	}
	// per-string bytes
	big := `{"description":"` + strings.Repeat("a", MaxFieldBytes+10) + `"}`
	fs, partial = FieldStrings(ToolView{InputSchema: json.RawMessage(big)})
	if partial == "" || len(fs[1].Text) != MaxFieldBytes || fs[1].Exact {
		t.Fatalf("byte cap: partial=%v len=%d exact=%v", partial, len(fs[1].Text), fs[1].Exact)
	}
	// total bytes
	huge := `{"description":"x","pad":"` + strings.Repeat("b", MaxSchemaBytes) + `"}`
	if _, partial := FieldStrings(ToolView{InputSchema: json.RawMessage(huge)}); partial == "" {
		t.Fatal("schema byte cap not reported")
	}
}

func TestFieldStrings_Deterministic(t *testing.T) {
	tool := ToolView{Description: "d", InputSchema: json.RawMessage(`{"properties":{"b":{"description":"B"},"a":{"description":"A"}}}`)}
	first, _ := FieldStrings(tool)
	for i := 0; i < 20; i++ {
		again, _ := FieldStrings(tool)
		if !reflect.DeepEqual(first, again) {
			t.Fatal("non-deterministic field order")
		}
	}
	if first[1].Path != "input_schema:/properties/b/description" {
		t.Fatalf("document order not kept: %q", first[1].Path)
	}
}

func TestFieldStrings_DescriptionIsBudgeted(t *testing.T) {
	fs, partial := FieldStrings(ToolView{Description: strings.Repeat("é", MaxFieldBytes/2+10)})
	if partial != "description longer than 64 KiB" || fs[0].Exact || len(fs[0].Text) > MaxFieldBytes || !utf8.ValidString(fs[0].Text) {
		t.Fatalf("partial=%q exact=%v len=%d", partial, fs[0].Exact, len(fs[0].Text))
	}
}
