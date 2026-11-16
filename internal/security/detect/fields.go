package detect

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Field is one string of a tool's metadata, inspected on its own (Spec 122
// FR-003): the description, or one string VALUE of the input or output schema.
// A check that reads fields never assembles a directive across two of them,
// and every finding can name the field it fell in.
//
// Path is "description", or "<input_schema|output_schema>:<json-pointer>"
// (RFC 6901: "~" → "~0", "/" → "~1"), e.g.
// "input_schema:/properties/query/description".
//
// RawStart/RawEnd are the byte range of the JSON string token (quotes
// included) in the raw schema text; for the description they are 0 and
// len(description). Exact reports that Text[i] sits at raw byte RawStart+1+i:
// true for the description and for a schema string with no escape sequence,
// false when the token carried an escape or was truncated. A highlight span
// inside a schema string is produced only for an exact field (FieldSpan): a
// mark on the wrong characters is worse than no mark.
type Field struct {
	Path     string
	Field    SpanField
	RawStart int
	RawEnd   int
	Text     string
	Exact    bool
}

// Inspection caps of FieldStrings. They bound SCAN INPUT and are separate from
// MaxEvidenceLen, which bounds rendered evidence. Hitting any of them makes
// FieldStrings report a non-empty partial reason; the caller decides how to
// surface that (padding.hidden emits one "analysis incomplete" review signal).
const (
	// MaxFieldDepth is the deepest nesting of containers walked in a schema.
	MaxFieldDepth = 32
	// MaxFieldsPerSchema is the most string values yielded per schema.
	MaxFieldsPerSchema = 512
	// MaxSchemaBytes is the most raw schema bytes walked.
	MaxSchemaBytes = 1 << 20
	// MaxFieldBytes is the most bytes of one string value handed to a check.
	MaxFieldBytes = 64 << 10
)

// FieldStrings returns the tool's fields in a fixed order: the description
// first, then every string value of the input schema in document (byte) order,
// then those of the output schema. Object KEYS are not fields. The walk is a
// hand-written tokenizer over the raw bytes (encoding/json gives no token
// offsets), linear in the schema size, total (a malformed schema yields the
// strings tokenized before the first error) and deterministic. partial names
// the first cap (or the malformed-JSON stop) that cut the walk short, and is
// empty when every string was inspected.
func FieldStrings(tool ToolView) (fields []Field, partial string) {
	desc := Field{
		Path:   string(SpanFieldDescription),
		Field:  SpanFieldDescription,
		RawEnd: len(tool.Description),
		Text:   tool.Description,
		Exact:  true,
	}
	if len(desc.Text) > MaxFieldBytes {
		// The description is budgeted like any other field (Sol r1 P2);
		// the existing checks still read it whole.
		cut := MaxFieldBytes
		for cut > 0 && !utf8.RuneStart(desc.Text[cut]) {
			cut--
		}
		desc.Text = desc.Text[:cut]
		desc.Exact = false
		partial = "description longer than 64 KiB"
	}
	fields = append(fields, desc)
	if len(tool.InputSchema) > 0 {
		fs, p := schemaStrings(SpanFieldInputSchema, tool.InputSchema)
		fields = append(fields, fs...)
		if partial == "" {
			partial = p
		}
	}
	if len(tool.OutputSchema) > 0 {
		fs, p := schemaStrings(SpanFieldOutputSchema, tool.OutputSchema)
		fields = append(fields, fs...)
		if partial == "" {
			partial = p
		}
	}
	return fields, partial
}

// FieldSpan builds the highlight span for a check that matched
// f.Text[byteStart:byteEnd], or nil when no honest span exists: the field is
// not exact, the range is not inside the text, or the conversion to UTF-16
// offsets against the RAW field text fails. tool supplies the raw texts.
func FieldSpan(checkID string, tier Tier, tool ToolView, f Field, byteStart, byteEnd int) []Span {
	if f.Field == SpanFieldDescription {
		return DescriptionSpan(checkID, tier, tool.Description, byteStart, byteEnd)
	}
	if !f.Exact || byteStart < 0 || byteEnd > len(f.Text) || byteStart >= byteEnd {
		return nil
	}
	var raw string
	switch f.Field {
	case SpanFieldInputSchema:
		raw = string(tool.InputSchema)
	case SpanFieldOutputSchema:
		raw = string(tool.OutputSchema)
	default:
		return nil
	}
	rs, re := f.RawStart+1+byteStart, f.RawStart+1+byteEnd
	if re > len(raw) || raw[rs:re] != f.Text[byteStart:byteEnd] {
		return nil
	}
	snippet, covered, truncated := CapSpanSnippet(f.Text[byteStart:byteEnd])
	if covered <= 0 {
		return nil
	}
	start, end, ok := UTF16Offsets(raw, rs, rs+covered)
	if !ok {
		return nil
	}
	return []Span{{
		Field:     f.Field,
		Start:     start,
		End:       end,
		CheckID:   checkID,
		Tier:      tier.String(),
		Snippet:   snippet,
		Truncated: truncated,
	}}
}

// frame is one open container of the schema walk.
type frame struct {
	object  bool
	key     string // current member key (object)
	index   int    // next element index (array)
	wantKey bool
	hasKey  bool
}

// schemaStrings tokenizes one raw schema and yields its string values.
func schemaStrings(sf SpanField, raw json.RawMessage) (fields []Field, partial string) {
	text := string(raw)
	if len(text) > MaxSchemaBytes {
		text = text[:MaxSchemaBytes]
		partial = "schema larger than 1 MiB"
	}
	prefix := string(sf) + ":"
	var stack []frame
	pathOf := func() string {
		var b strings.Builder
		b.WriteString(prefix)
		for _, fr := range stack {
			b.WriteByte('/')
			if fr.object {
				b.WriteString(escapePointer(fr.key))
			} else {
				b.WriteString(strconv.Itoa(fr.index))
			}
		}
		return b.String()
	}
	// afterValue advances the enclosing frame past a completed value.
	afterValue := func() {
		if len(stack) == 0 {
			return
		}
		top := &stack[len(stack)-1]
		if top.object {
			top.hasKey = false
		} else {
			top.index++
		}
	}
	i := 0
	for i < len(text) {
		c := text[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			i++
		case '{', '[':
			if len(stack) >= MaxFieldDepth {
				return fields, "schema nested deeper than 32 levels"
			}
			stack = append(stack, frame{object: c == '{', wantKey: c == '{'})
			i++
		case '}', ']':
			if len(stack) == 0 {
				return fields, partial
			}
			stack = stack[:len(stack)-1]
			afterValue()
			i++
		case ',':
			if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].wantKey = true
			}
			i++
		case ':':
			if len(stack) > 0 && stack[len(stack)-1].object {
				stack[len(stack)-1].wantKey = false
			}
			i++
		case '"':
			end, decoded, exact, ok := scanJSONString(text, i)
			if !ok {
				return fields, "malformed schema JSON"
			}
			top := (*frame)(nil)
			if len(stack) > 0 {
				top = &stack[len(stack)-1]
			}
			if top != nil && top.object && (top.wantKey || !top.hasKey) {
				top.key = decoded
				top.hasKey = true
				top.wantKey = false
				i = end
				continue
			}
			if len(fields) >= MaxFieldsPerSchema {
				return fields, "schema with more than 512 strings"
			}
			f := Field{Path: pathOf(), Field: sf, RawStart: i, RawEnd: end, Text: decoded, Exact: exact}
			if len(f.Text) > MaxFieldBytes {
				cut := MaxFieldBytes
				for cut > 0 && !utf8.RuneStart(f.Text[cut]) {
					cut--
				}
				f.Text = f.Text[:cut]
				f.Exact = false
				if partial == "" {
					partial = "schema string longer than 64 KiB"
				}
			}
			fields = append(fields, f)
			afterValue()
			i = end
		default:
			// number, true, false, null: skip the scalar
			if !strings.ContainsRune("-0123456789tfn", rune(c)) {
				return fields, "malformed schema JSON"
			}
			j := i
			for j < len(text) && !strings.ContainsRune(",}] \t\n\r", rune(text[j])) {
				j++
			}
			if j == i {
				return fields, "malformed schema JSON"
			}
			afterValue()
			i = j
		}
	}
	return fields, partial
}

// scanJSONString scans the string token starting at the quote text[i]. It
// returns the index after the closing quote, the decoded value, whether the
// token had no escape (so decoded offsets map 1:1 onto raw offsets) and
// whether the token was well-formed.
func scanJSONString(text string, i int) (end int, decoded string, exact bool, ok bool) {
	j := i + 1
	escaped := false
	for j < len(text) {
		c := text[j]
		if c == '\\' {
			escaped = true
			j += 2
			continue
		}
		if c == '"' {
			tok := text[i : j+1]
			if !escaped {
				return j + 1, text[i+1 : j], true, true
			}
			var s string
			if err := json.Unmarshal([]byte(tok), &s); err != nil {
				return 0, "", false, false
			}
			return j + 1, s, false, true
		}
		if c < 0x20 {
			return 0, "", false, false // raw control byte: not a JSON string
		}
		j++
	}
	return 0, "", false, false
}

// escapePointer applies RFC 6901 escaping to one reference token.
func escapePointer(s string) string {
	if !strings.ContainsAny(s, "~/") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
