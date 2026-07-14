package toonenc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// parseJSON decodes a JSON document the same way the encoder does
// (json.Number, single value).
func parseJSON(t *testing.T, text string) interface{} {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("fixture is not valid JSON: %v\n%s", err, text)
	}
	return v
}

// uniformRows builds a JSON array of n uniform flat objects.
func uniformRows(n int) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":`)
		sb.WriteString(json.Number(itoa(i)).String())
		sb.WriteString(`,"name":"user-`)
		sb.WriteString(itoa(i))
		sb.WriteString(`","active":`)
		if i%2 == 0 {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
		sb.WriteString(`,"note":null}`)
	}
	sb.WriteString("]")
	return sb.String()
}

func itoa(i int) string {
	return json.Number(strings.TrimSpace(jsonInt(i))).String()
}

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestClassifyTabularUniformArray(t *testing.T) {
	c := Classify(parseJSON(t, uniformRows(4)))
	if !c.Tabular {
		t.Fatalf("expected tabular, got %+v", c)
	}
	if c.Envelope {
		t.Fatalf("bare array must not be flagged as envelope: %+v", c)
	}
	if c.Rows != 4 || c.Cols != 4 {
		t.Fatalf("expected 4 rows / 4 cols, got %+v", c)
	}
}

func TestClassifyEnvelope(t *testing.T) {
	c := Classify(parseJSON(t, `{"items":`+uniformRows(5)+`}`))
	if !c.Tabular || !c.Envelope {
		t.Fatalf("single-key envelope over a qualifying array must classify tabular+envelope, got %+v", c)
	}
	if c.Rows != 5 || c.Cols != 4 {
		t.Fatalf("expected 5 rows / 4 cols, got %+v", c)
	}
}

func TestClassifyEnvelopeNonArrayValue(t *testing.T) {
	c := Classify(parseJSON(t, `{"item":{"id":1}}`))
	if c.Tabular || c.Reason != ReasonNotArray {
		t.Fatalf("single-key object wrapping a non-array must be ReasonNotArray, got %+v", c)
	}
}

func TestClassifyMultiKeyObjectNotEnvelope(t *testing.T) {
	c := Classify(parseJSON(t, `{"items":`+uniformRows(5)+`,"total":5}`))
	if c.Tabular || c.Reason != ReasonNotArray {
		t.Fatalf("two-key object must not qualify as envelope, got %+v", c)
	}
}

func TestClassifyRejections(t *testing.T) {
	tests := []struct {
		name   string
		json   string
		reason NotTabularReason
	}{
		{"too few rows", uniformRows(3), ReasonTooFewRows},
		{"empty array", `[]`, ReasonTooFewRows},
		{"non-object elements", `[1,2,3,4]`, ReasonNonObjectElements},
		{"mixed elements", `[{"a":1},{"a":2},{"a":3},4]`, ReasonNonObjectElements},
		{"nested object value", `[{"a":{"x":1}},{"a":{"x":2}},{"a":{"x":3}},{"a":{"x":4}}]`, ReasonNestedValues},
		{"nested array value", `[{"a":[1]},{"a":[2]},{"a":[3]},{"a":[4]}]`, ReasonNestedValues},
		{"scalar", `42`, ReasonNotArray},
		{"string", `"hello"`, ReasonNotArray},
		{"null", `null`, ReasonNotArray},
		{"bool", `true`, ReasonNotArray},
		{"plain object", `{"a":1,"b":2}`, ReasonNotArray},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Classify(parseJSON(t, tt.json))
			if c.Tabular {
				t.Fatalf("expected not tabular, got %+v", c)
			}
			if c.Reason != tt.reason {
				t.Fatalf("expected reason %q, got %q (%+v)", tt.reason, c.Reason, c)
			}
		})
	}
}

// TestClassifyRaggedTolerance: a key missing from at most 10% of rows is
// tolerated (still tabular, key counted in the union set); a key missing from
// more than 10% of rows makes the array too ragged.
func TestClassifyRaggedTolerance(t *testing.T) {
	// 10 rows, "extra" present in 9/10 = 90% → tolerated.
	rows := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		if i == 0 {
			rows = append(rows, `{"id":`+jsonInt(i)+`}`)
		} else {
			rows = append(rows, `{"id":`+jsonInt(i)+`,"extra":"x"}`)
		}
	}
	c := Classify(parseJSON(t, "["+strings.Join(rows, ",")+"]"))
	if !c.Tabular {
		t.Fatalf("90%%-present key must be tolerated, got %+v", c)
	}
	if c.Rows != 10 || c.Cols != 2 {
		t.Fatalf("expected 10 rows / 2 cols, got %+v", c)
	}

	// 10 rows, "extra" present in 8/10 = 80% → too ragged.
	rows = rows[:0]
	for i := 0; i < 10; i++ {
		if i < 2 {
			rows = append(rows, `{"id":`+jsonInt(i)+`}`)
		} else {
			rows = append(rows, `{"id":`+jsonInt(i)+`,"extra":"x"}`)
		}
	}
	c = Classify(parseJSON(t, "["+strings.Join(rows, ",")+"]"))
	if c.Tabular || c.Reason != ReasonTooRagged {
		t.Fatalf("80%%-present key must be ReasonTooRagged, got %+v", c)
	}
}

// TestClassifyEmptyObjects: rows with no keys collapse the union key set —
// nothing tabular to encode.
func TestClassifyEmptyObjects(t *testing.T) {
	c := Classify(parseJSON(t, `[{},{},{},{}]`))
	if c.Tabular || c.Reason != ReasonTooRagged {
		t.Fatalf("empty-object rows must be ReasonTooRagged, got %+v", c)
	}
}

// TestClassifyKeyOrderIrrelevant: the same rows serialized with different key
// orders classify identically (FR-003b: key order is irrelevant; FR-011).
func TestClassifyKeyOrderIrrelevant(t *testing.T) {
	a := Classify(parseJSON(t, `[{"a":1,"b":2},{"a":1,"b":2},{"a":1,"b":2},{"b":2,"a":1}]`))
	b := Classify(parseJSON(t, `[{"b":2,"a":1},{"b":2,"a":1},{"b":2,"a":1},{"a":1,"b":2}]`))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("classification must be key-order independent: %+v vs %+v", a, b)
	}
	if !a.Tabular || a.Cols != 2 {
		t.Fatalf("expected tabular with 2 cols, got %+v", a)
	}
}

// TestClassifyDeterministic: classifying the same value twice yields an
// identical Classification (FR-011).
func TestClassifyDeterministic(t *testing.T) {
	v := parseJSON(t, uniformRows(20))
	a := Classify(v)
	b := Classify(v)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Classify is not deterministic: %+v vs %+v", a, b)
	}
}
