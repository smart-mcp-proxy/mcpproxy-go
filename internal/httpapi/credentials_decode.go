package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Sanitized request decoding for the two credential issuance routes (Spec 115
// FR-020b, data-model §8.3). encoding/json's own messages can quote a caller's
// key (`json: unknown field "<key>"`) or, for some inputs, a literal; on
// POST /api/v1/tokens and POST /api/v1/clients those messages would echo a
// pasted credential before the service screen runs. decodeIssuanceBody maps
// every decoder error to a fixed taxonomy that names only the route's OWN
// known field names. Per-route strictness is unchanged: the clients route
// refuses unknown fields, the tokens route ignores them.

// Issuance decode error kinds.
const (
	IssuanceDecodeUnknownField = "unknown_field"
	IssuanceDecodeWrongType    = "wrong_type"
	IssuanceDecodeSyntax       = "syntax"
	IssuanceDecodeEmpty        = "empty"
	IssuanceDecodeOther        = "other"
)

// IssuanceDecodeError is a sanitized decode failure: Kind is one of the
// IssuanceDecode* kinds, Field a KNOWN field name or the fixed placeholder,
// Offset the byte offset of a syntax error. It never carries decoder text.
type IssuanceDecodeError struct {
	Kind   string
	Field  string
	Offset int64
}

func (e *IssuanceDecodeError) Error() string {
	switch e.Kind {
	case IssuanceDecodeUnknownField:
		return "invalid request body: unrecognised field (its name is not echoed)"
	case IssuanceDecodeWrongType:
		if e.Field != internalRuntime.UnknownArgumentField && e.Field != "" {
			return fmt.Sprintf("invalid request body: field %q has the wrong type", e.Field)
		}
		return "invalid request body: a field has the wrong type"
	case IssuanceDecodeSyntax:
		return fmt.Sprintf("invalid request body: malformed JSON at byte %d", e.Offset)
	case IssuanceDecodeEmpty:
		return "request body is required"
	default:
		return "invalid request body"
	}
}

// jsonFieldNames returns the top-level JSON field names of the struct v points
// to: the only names a decode error may echo.
func jsonFieldNames(v interface{}) map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = true
	}
	return out
}

// decodeIssuanceBody decodes one JSON object into v. strict refuses unknown
// fields (clients route); otherwise they are dropped (tokens route) and their
// values are never stored, logged or echoed. Trailing data is refused.
func decodeIssuanceBody(r *http.Request, v interface{}, strict bool) *IssuanceDecodeError {
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		return classifyIssuanceDecodeError(err, jsonFieldNames(v))
	}
	if dec.More() {
		return &IssuanceDecodeError{Kind: IssuanceDecodeOther}
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return &IssuanceDecodeError{Kind: IssuanceDecodeOther}
	}
	return nil
}

func classifyIssuanceDecodeError(err error, known map[string]bool) *IssuanceDecodeError {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	switch {
	case errors.Is(err, io.EOF):
		return &IssuanceDecodeError{Kind: IssuanceDecodeEmpty}
	case errors.As(err, &syntaxErr):
		return &IssuanceDecodeError{Kind: IssuanceDecodeSyntax, Offset: syntaxErr.Offset}
	case errors.As(err, &typeErr):
		// Only the FIRST path segment is compared with the route's own field
		// names; Value (which can carry a literal) is never used.
		first, _, _ := strings.Cut(typeErr.Field, ".")
		field := internalRuntime.UnknownArgumentField
		if known[first] {
			field = first
		}
		return &IssuanceDecodeError{Kind: IssuanceDecodeWrongType, Field: field}
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return &IssuanceDecodeError{Kind: IssuanceDecodeUnknownField, Field: internalRuntime.UnknownArgumentField}
	case errors.Is(err, io.ErrUnexpectedEOF):
		return &IssuanceDecodeError{Kind: IssuanceDecodeSyntax}
	}
	return &IssuanceDecodeError{Kind: IssuanceDecodeOther}
}
