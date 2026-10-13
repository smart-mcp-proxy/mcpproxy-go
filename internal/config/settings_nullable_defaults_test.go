package config

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Spec 109 D35 (T161): the Settings page renders a toggle for every nullable
// (*bool) config key. A nil *bool resolves to a concrete value on the Go side
// (quarantine on, telemetry on, ...). The shared fixture records that value so
// the Web UI (frontend/tests/unit/settings-nullable-defaults.spec.ts) renders
// the same state the core enforces, and so a new *bool key cannot be added
// without deciding which of the two sets it belongs to.

type nullableDefaultsFixture struct {
	Defaults      map[string]json.RawMessage `json:"defaults"`
	NotInSettings []string                   `json:"not_in_settings"`
}

// blockDependentDefault is the fixture shape for the audit_log keys, whose
// resolved default depends on whether the audit_log block is present, and (for
// an absent block) on the edition the binary was built as.
type blockDependentDefault struct {
	AbsentBlock  map[string]bool `json:"absent_block"`
	PresentBlock bool            `json:"present_block"`
}

func loadNullableDefaultsFixture(t *testing.T) nullableDefaultsFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/settings_nullable_defaults.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx nullableDefaultsFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return fx
}

func fixtureBool(t *testing.T, fx nullableDefaultsFixture, key string) bool {
	t.Helper()
	raw, ok := fx.Defaults[key]
	if !ok {
		t.Fatalf("fixture has no default for %q", key)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("fixture default for %q is not a bool: %v", key, err)
	}
	return b
}

func fixtureBlock(t *testing.T, fx nullableDefaultsFixture, key string) blockDependentDefault {
	t.Helper()
	raw, ok := fx.Defaults[key]
	if !ok {
		t.Fatalf("fixture has no default for %q", key)
	}
	var d blockDependentDefault
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("fixture default for %q is not block-dependent: %v", key, err)
	}
	return d
}

func TestSettingsNullableDefaultsMatchGoResolvers(t *testing.T) {
	fx := loadNullableDefaultsFixture(t)

	t.Run("quarantine_enabled", func(t *testing.T) {
		cfg := &Config{}
		if got, want := cfg.IsQuarantineEnabled(), fixtureBool(t, fx, "quarantine_enabled"); got != want {
			t.Fatalf("nil quarantine_enabled resolves to %v, fixture says %v", got, want)
		}
	})

	t.Run("telemetry.enabled", func(t *testing.T) {
		t.Setenv("MCPPROXY_TELEMETRY", "")
		want := fixtureBool(t, fx, "telemetry.enabled")
		if got := (&Config{}).IsTelemetryEnabled(); got != want {
			t.Fatalf("absent telemetry block resolves to %v, fixture says %v", got, want)
		}
		if got := (&Config{Telemetry: &TelemetryConfig{}}).IsTelemetryEnabled(); got != want {
			t.Fatalf("nil telemetry.enabled resolves to %v, fixture says %v", got, want)
		}
	})

	t.Run("audit_log present block", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Path: "/tmp/audit.jsonl"}}
		resolved, _, err := EffectiveAuditLog(cfg, TransportHTTP)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := fixtureBlock(t, fx, "audit_log.enabled").PresentBlock; resolved.Enabled != want {
			t.Errorf("present-block audit_log.enabled = %v, fixture says %v", resolved.Enabled, want)
		}
		if want := fixtureBlock(t, fx, "audit_log.compress").PresentBlock; resolved.Compress != want {
			t.Errorf("present-block audit_log.compress = %v, fixture says %v", resolved.Compress, want)
		}
		if want := fixtureBlock(t, fx, "audit_log.stdout").PresentBlock; resolved.Stdout != want {
			t.Errorf("present-block audit_log.stdout = %v, fixture says %v", resolved.Stdout, want)
		}
	})
}

// collectNullableBoolPaths walks a struct type and returns the dot-path of
// every *bool reachable through struct (or pointer-to-struct) fields. Slices
// and maps are not followed: per-server and per-profile structs are edited on
// their own pages, not through the Settings catalogue.
func collectNullableBoolPaths(t reflect.Type, prefix string, seen map[reflect.Type]bool, out *[]string) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Bool {
			*out = append(*out, path)
			continue
		}
		if ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Struct {
			collectNullableBoolPaths(ft, path, seen, out)
		}
	}
}

func TestEveryNullableBoolConfigPathIsClassified(t *testing.T) {
	fx := loadNullableDefaultsFixture(t)
	classified := map[string]string{}
	for k := range fx.Defaults {
		classified[k] = "defaults"
	}
	for _, k := range fx.NotInSettings {
		if prev, dup := classified[k]; dup {
			t.Errorf("%q is listed in both %s and not_in_settings", k, prev)
		}
		classified[k] = "not_in_settings"
	}

	var paths []string
	collectNullableBoolPaths(reflect.TypeOf(Config{}), "", map[reflect.Type]bool{}, &paths)
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("reflection found no *bool paths; the walker is broken")
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[p] = true
		if _, ok := classified[p]; !ok {
			t.Errorf("*bool config path %q is not classified in testdata/settings_nullable_defaults.json: "+
				"add it to `defaults` (and give its Settings toggle a defaultValue in frontend/src/views/settings/fields.ts) "+
				"or to `not_in_settings`", p)
		}
	}
	for k := range classified {
		if !found[k] {
			t.Errorf("fixture lists %q but Config has no such *bool path (stale entry)", k)
		}
	}
}
