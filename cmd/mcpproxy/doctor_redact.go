package main

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// doctorRedactionMask replaces credential query params, URL userinfo passwords
// and the literal admin key in every `mcpproxy doctor` output format. The
// report is meant to be pasted into issues and chats, so there is no opt-out;
// `mcpproxy status --show-key` / `--web-url` are the explicit ways to get the
// key.
const doctorRedactionMask = "REDACTED"

// doctorSecretLiterals holds secrets (the admin API key) that must never appear
// in doctor output even outside a URL. runDoctor sets it for the run.
var doctorSecretLiterals []string

// An apostrophe is legal inside a URL (`?label=Bob's`), so the body only stops
// at whitespace, double quote or angle brackets; a trailing apostrophe that
// merely closes a single-quoted URL is trimmed in redactDoctorString.
var doctorURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"<>]+`)

func doctorMask(string) string { return doctorRedactionMask }

// redactDoctorString masks credentials in every URL embedded in s, then scrubs
// each registered secret literal.
func redactDoctorString(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(s, "://") {
		s = doctorURLPattern.ReplaceAllStringFunc(s, func(u string) string {
			trail := ""
			if strings.HasSuffix(u, "'") {
				u, trail = strings.TrimSuffix(u, "'"), "'"
			}
			return oauth.RedactURLQueryParamsWith(u, doctorMask) + trail
		})
	}
	for _, lit := range doctorSecretLiterals {
		if lit != "" {
			s = strings.ReplaceAll(s, lit, doctorRedactionMask)
		}
	}
	return s
}

// redactDoctorValue walks generic JSON values (maps, slices, strings) and
// returns a redacted copy; other scalars pass through.
func redactDoctorValue(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		return redactDoctorString(t)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = redactDoctorValue(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = redactDoctorValue(val)
		}
		return out
	default:
		return v
	}
}

// redactDoctorTyped redacts a typed payload (attention response, profile
// checks, ...) through its JSON form. When nothing needs redacting the original
// value is returned untouched, so field order in the JSON output is unchanged.
func redactDoctorTyped[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return v
	}
	redacted := redactDoctorValue(generic)
	if reflect.DeepEqual(generic, redacted) {
		return v
	}
	rawRedacted, err := json.Marshal(redacted)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(rawRedacted, &out); err != nil {
		return v
	}
	return out
}

func redactDoctorMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	return redactDoctorValue(m).(map[string]interface{})
}
