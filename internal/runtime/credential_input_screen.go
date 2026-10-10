package runtime

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// The secret-shaped input screen (Spec 115 FR-020a/FR-020b, data-model §8). It
// runs FIRST on every credential issuance, on every surface, so a credential,
// the API key or a detector-flagged secret pasted into issuance metadata
// (purpose, display_name, an id, an expiry, a profile alias, a REST array
// element) is refused before anything is generated, stored, audited, echoed
// or logged.

// MaxCredentialArgumentsBytes caps the canonical JSON of a `credentials` call's
// arguments (A17). The largest legitimate call is a 500-character purpose plus
// a few ids, well under 2 KiB; capping at 16 KiB guarantees every screened
// string is scanned whole by the forced detector below.
const MaxCredentialArgumentsBytes = 16 * 1024

// UnknownArgumentField is the fixed placeholder a refusal names instead of a
// caller-supplied key (FR-020a): a key is never echoed.
const UnknownArgumentField = "(unknown argument)"

// minScreenAPIKeyLen is the shortest configured API key the screen compares
// against. Auto-generated keys are 64 hex characters; a hand-set key shorter
// than this would turn every argument containing it into a false positive,
// so it is skipped (documented assumption in spec.md).
const minScreenAPIKeyLen = 8

// SecretInputError is the `secret_in_argument` refusal. Fields are the KNOWN
// offending argument names (sorted, deduplicated); UnknownCount counts the
// offending arguments whose names are not known (never named).
type SecretInputError struct {
	Fields       []string
	UnknownCount int
}

// Field is the first offending known argument, or the fixed placeholder.
func (e *SecretInputError) Field() string {
	if len(e.Fields) > 0 {
		return e.Fields[0]
	}
	return UnknownArgumentField
}

// Error names the field, never the value.
func (e *SecretInputError) Error() string {
	if len(e.Fields) == 0 {
		return "an unrecognised argument looks like a credential or secret; it was not stored. Remove it and retry"
	}
	return fmt.Sprintf("argument %q looks like a credential or secret; it was not stored. Remove it and retry", e.Field())
}

// Code is the wire `code`.
func (e *SecretInputError) Code() string { return profile.CredentialErrorCodeSecretInArgument }

// ArgumentsTooLargeError is the `arguments_too_large` refusal (A17).
type ArgumentsTooLargeError struct{ Size int }

func (e *ArgumentsTooLargeError) Error() string {
	return fmt.Sprintf("arguments are too large (limit %d bytes); they were not stored", MaxCredentialArgumentsBytes)
}

// Code is the wire `code`.
func (e *ArgumentsTooLargeError) Code() string { return profile.CredentialErrorCodeArgumentsTooLarge }

// credentialScreenDetectorConfig builds the screening detector's config from
// the live sensitive_data_detection config (spec review r3, A17). Only the
// custom patterns and the entropy threshold are copied; enablement, request
// scanning, every category and a payload limit above the size cap are FORCED,
// so a user who turned detection off for upstream traffic never weakens the
// credential-leak screen of admin metadata.
func credentialScreenDetectorConfig(live *config.SensitiveDataDetectionConfig) *config.SensitiveDataDetectionConfig {
	out := config.DefaultSensitiveDataDetectionConfig()
	if live != nil {
		out.CustomPatterns = append([]config.CustomPattern(nil), live.CustomPatterns...)
		out.SensitiveKeywords = append([]string(nil), live.SensitiveKeywords...)
		if live.EntropyThreshold > 0 {
			out.EntropyThreshold = live.EntropyThreshold
		}
	}
	out.Enabled = true
	out.ScanRequests = true
	// Every category on, built-in and custom: an empty map means "all".
	out.Categories = nil
	out.MaxPayloadSizeKB = MaxCredentialArgumentsBytes/1024 + 1
	return out
}

// CredentialScreen is one evaluation context of the screen: the forced
// detector and the live API key.
type CredentialScreen struct {
	detector *security.Detector
	apiKey   string
}

// NewCredentialScreen builds the screen for the live config.
func NewCredentialScreen(cfg *config.Config) *CredentialScreen {
	var live *config.SensitiveDataDetectionConfig
	apiKey := ""
	if cfg != nil {
		live = cfg.SensitiveDataDetection
		apiKey = cfg.APIKey
	}
	return &CredentialScreen{detector: security.NewDetector(credentialScreenDetectorConfig(live)), apiKey: apiKey}
}

// SecretShaped reports whether s carries an issued-credential prefix, the
// configured API key, or anything the forced detector flags. A truncated scan
// is treated as a hit (fail closed).
func (c *CredentialScreen) SecretShaped(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, auth.TokenPrefixStr) || strings.Contains(lower, auth.ClientTokenPrefixStr) {
		return true
	}
	if len(c.apiKey) >= minScreenAPIKeyLen && containsConstantTime(s, c.apiKey) {
		return true
	}
	if c.detector == nil {
		return false
	}
	res := c.detector.Scan(s, "")
	return res != nil && (res.Detected || res.Truncated)
}

// containsConstantTime reports whether needle occurs in haystack, comparing
// every window in constant time so the comparison does not leak the key.
func containsConstantTime(haystack, needle string) bool {
	n := len(needle)
	if n == 0 || len(haystack) < n {
		return false
	}
	found := 0
	for i := 0; i+n <= len(haystack); i++ {
		found |= subtle.ConstantTimeCompare([]byte(haystack[i:i+n]), []byte(needle))
	}
	return found == 1
}

// ScreenFields screens named string values (the service-side screen of the
// persisted issuance fields, data-model §8.2). Every hit is collected; a field
// listed several times (an array) is reported once.
func (c *CredentialScreen) ScreenFields(fields []ScreenField) *SecretInputError {
	hit := map[string]bool{}
	for _, f := range fields {
		for _, v := range f.Values {
			if c.SecretShaped(v) {
				hit[f.Name] = true
				break
			}
		}
	}
	if len(hit) == 0 {
		return nil
	}
	names := make([]string, 0, len(hit))
	for n := range hit {
		names = append(names, n)
	}
	sort.Strings(names)
	return &SecretInputError{Fields: names}
}

// ScreenField is one named, caller-supplied field and its raw value(s).
type ScreenField struct {
	Name   string
	Values []string
}

// ScreenArguments screens a whole decoded MCP argument object (data-model §8):
// every key and every value at every depth, whatever its JSON type, plus the
// canonical JSON of each top-level argument. known names the argument names
// that may be echoed; any other offending argument is only counted.
func (c *CredentialScreen) ScreenArguments(args map[string]any, known map[string]bool) *SecretInputError {
	var fields []string
	unknown := 0
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		offends := c.SecretShaped(k) || c.walk(args[k])
		if !offends {
			if raw, err := json.Marshal(args[k]); err == nil {
				offends = c.SecretShaped(string(raw))
			}
		}
		if !offends {
			continue
		}
		if known[k] {
			fields = append(fields, k)
		} else {
			unknown++
		}
	}
	if len(fields) == 0 && unknown == 0 {
		return nil
	}
	return &SecretInputError{Fields: fields, UnknownCount: unknown}
}

// walk reports whether any key or value under v is secret-shaped.
func (c *CredentialScreen) walk(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return c.SecretShaped(x)
	case bool:
		return c.SecretShaped(strconv.FormatBool(x))
	case float64:
		return c.SecretShaped(strconv.FormatFloat(x, 'f', -1, 64)) || c.SecretShaped(strconv.FormatFloat(x, 'g', -1, 64))
	case json.Number:
		return c.SecretShaped(x.String())
	case map[string]any:
		for k, e := range x {
			if c.SecretShaped(k) || c.walk(e) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range x {
			if c.walk(e) {
				return true
			}
		}
		return false
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return true // unserializable: fail closed
		}
		return c.SecretShaped(string(raw))
	}
}

// CanonicalArgumentsSize is the byte length of the canonical JSON of args
// (encoding/json sorts map keys). An unserializable payload reports a size
// above the cap so it is refused (fail closed).
func CanonicalArgumentsSize(args map[string]any) int {
	raw, err := json.Marshal(args)
	if err != nil {
		return MaxCredentialArgumentsBytes + 1
	}
	return len(raw)
}
