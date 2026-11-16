# Contract: `detect.FieldStrings`

```go
type Field struct {
    Path     string    // "description" | "input_schema:<ptr>" | "output_schema:<ptr>"
    Field    SpanField
    RawStart int       // byte offset of the opening quote in the raw schema (0 for description)
    RawEnd   int       // byte offset after the closing quote (len(description) for description)
    Text     string    // decoded value
    Exact    bool      // Text[i] ↔ raw[RawStart+1+i]
}
func FieldStrings(tool ToolView) []Field
func SchemaSpan(checkID string, tier Tier, f Field, byteStart, byteEnd int) []Span
```

- Pure, deterministic, no allocation proportional to anything but the input.
- Only string VALUES are yielded; object keys are not.
- Pointer escaping per RFC 6901: `~` → `~0`, `/` → `~1`.
- Caps: `maxFieldDepth = 32`, `maxFieldsPerSchema = 512`, `maxFieldBytes = 64 << 10`. A schema that is
  not valid JSON yields the strings tokenized before the first error.
- `Exact` is false when the token contains any backslash escape or was truncated.
- `SchemaSpan` returns nil unless `f.Exact` and the range is inside `f.Text`; offsets are converted
  against the RAW schema text so the Web UI's existing `input_schema` rendering slices correctly.
