package telemetry

import (
	"errors"
	"strings"
	"testing"
)

// Spec 126 FR-029 (T013): ScanForPII treats upstream_outcomes and day_two as
// closed objects: closed key sets, closed enums, string buckets only, no
// nested maps, no timestamps, and a violation never echoes the offending key.

const (
	validOutcomes = `{"succeeded":"1-10","unusable":"0","succeeded_non_starter":"1-10","coverage":"full"}`
	validDayTwo   = `{"install_origin":"fresh","install_age":"d0_6","success_scope_ever":"non_starter"}`
)

func v17Payload(outcomes, dayTwo string) []byte {
	b := `{"anonymous_id":"abc","schema_version":17`
	if outcomes != "" {
		b += `,"upstream_outcomes":` + outcomes
	}
	if dayTwo != "" {
		b += `,"day_two":` + dayTwo
	}
	return []byte(b + `}`)
}

func TestScanForPII_AcceptsValidDayTwo(t *testing.T) {
	accepted := [][]byte{
		v17Payload(validOutcomes, validDayTwo),
		v17Payload(validOutcomes, `{"install_origin":"existing","success_scope_ever":"none"}`),
		v17Payload(`{"succeeded":"1000+","unusable":"101-1000","succeeded_non_starter":"11-100","coverage":"partial"}`, ""),
		v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","window_age":"d7_13"}`, ""),
		v17Payload("", `{"install_origin":"fresh","install_age":"d14_plus","success_scope_ever":"starter_only"}`),
		v17Payload("", `{"install_origin":"fresh","install_age":"d7_13","success_scope_ever":"none"}`),
		v17Payload("", ""),
	}
	for _, p := range accepted {
		if err := ScanForPII(p); err != nil {
			t.Errorf("valid payload rejected: %s: %v", p, err)
		}
	}
}

func TestScanForPII_RejectsInvalidUpstreamOutcomesDayTwo(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		// secret is a string that must never appear in the violation.
		secret string
	}{
		// unknown keys
		{"outcomes unknown key", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","github_calls":"0"}`, ""), "github_calls"},
		{"day_two unknown key", v17Payload("", `{"install_origin":"fresh","install_age":"d0_6","success_scope_ever":"none","acme-internal":"x"}`), "acme-internal"},
		// out-of-enum values, every field
		{"succeeded out of enum", v17Payload(`{"succeeded":"5","unusable":"0","succeeded_non_starter":"0","coverage":"full"}`, ""), ""},
		{"unusable out of enum", v17Payload(`{"succeeded":"0","unusable":"lots","succeeded_non_starter":"0","coverage":"full"}`, ""), ""},
		{"succeeded_non_starter out of enum", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"","coverage":"full"}`, ""), ""},
		{"window_age out of enum", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","window_age":"d3"}`, ""), ""},
		{"window_age timestamp", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","window_age":"2026-11-18T09:30:00Z"}`, ""), "2026-11-18T09:30:00Z"},
		{"coverage out of enum", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"none"}`, ""), ""},
		{"install_origin out of enum", v17Payload("", `{"install_origin":"upgraded","success_scope_ever":"none"}`), ""},
		{"install_age out of enum", v17Payload("", `{"install_origin":"fresh","install_age":"d3","success_scope_ever":"none"}`), ""},
		{"success_scope_ever out of enum", v17Payload("", `{"install_origin":"fresh","install_age":"d0_6","success_scope_ever":"all"}`), ""},
		// install_age present only when fresh; required keys present
		{"install_age on existing", v17Payload("", `{"install_origin":"existing","install_age":"d0_6","success_scope_ever":"none"}`), ""},
		{"install_age missing on fresh", v17Payload("", `{"install_origin":"fresh","success_scope_ever":"none"}`), ""},
		{"outcomes missing key", v17Payload(`{"succeeded":"0","unusable":"0","coverage":"full"}`, ""), ""},
		{"day_two missing scope", v17Payload("", `{"install_origin":"existing"}`), ""},
		// non-string bucket
		{"numeric bucket", v17Payload(`{"succeeded":3,"unusable":"0","succeeded_non_starter":"0","coverage":"full"}`, ""), ""},
		{"boolean origin", v17Payload("", `{"install_origin":true,"success_scope_ever":"none"}`), ""},
		{"null bucket", v17Payload(`{"succeeded":null,"unusable":"0","succeeded_non_starter":"0","coverage":"full"}`, ""), ""},
		// nested maps
		{"nested map in outcomes", v17Payload(`{"succeeded":{"github":"1-10"},"unusable":"0","succeeded_non_starter":"0","coverage":"full"}`, ""), "github"},
		{"nested map in day_two", v17Payload("", `{"install_origin":"fresh","install_age":{"d0_6":true},"success_scope_ever":"none"}`), ""},
		{"outcomes is a list", v17Payload(`["0"]`, ""), ""},
		{"outcomes is null", v17Payload(`null`, ""), ""},
		{"day_two is null", v17Payload("", `null`), ""},
		{"day_two is a string", v17Payload("", `"fresh"`), ""},
		// timestamps
		{"timestamp in day_two", v17Payload("", `{"install_origin":"fresh","install_age":"2026-11-18T09:30:00Z","success_scope_ever":"none"}`), "2026-11-18T09:30:00Z"},
		{"timestamp in outcomes", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"2026-11-18T09:30:00Z"}`, ""), "2026-11-18T09:30:00Z"},
		// server-name-like strings
		{"server name as value", v17Payload("", `{"install_origin":"fresh","install_age":"d0_6","success_scope_ever":"github-mcp"}`), "github-mcp"},
		// window_age only for a fresh install
		{"window_age on an existing install", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","window_age":"d0_6"}`, `{"install_origin":"existing","success_scope_ever":"none"}`), ""},
		{"server name as key", v17Payload(`{"succeeded":"0","unusable":"0","succeeded_non_starter":"0","coverage":"full","my-server":"1-10"}`, ""), "my-server"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ScanForPII(c.payload)
			if err == nil {
				t.Fatalf("payload accepted: %s", c.payload)
			}
			var v *AnonymityViolation
			if !errors.As(err, &v) {
				t.Fatalf("want *AnonymityViolation, got %T: %v", err, err)
			}
			if v.Rule != "day_two_field_invalid" {
				t.Fatalf("rule = %q, want day_two_field_invalid", v.Rule)
			}
			if c.secret != "" {
				for _, s := range []string{v.Pattern, v.Reason, err.Error()} {
					if strings.Contains(s, c.secret) {
						t.Fatalf("violation echoes the offending content %q: %q", c.secret, s)
					}
				}
			}
		})
	}
}
