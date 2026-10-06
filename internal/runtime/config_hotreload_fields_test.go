package runtime

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// #1435: an edit that DetectConfigChanges does not recognise computes an empty
// ChangedFields and is answered "No configuration changes detected" — a silent
// no-op the Settings UIs render as success. This guard mutates every top-level
// field of config.Config in isolation and asserts the diff notices it.
//
// configFieldsNotDiffed lists the fields that are intentionally NOT diffed,
// each with the reason. Adding a config field forces a choice: teach
// DetectConfigChanges about it, or document here why an edit is not an
// applicable change.
var configFieldsNotDiffed = map[string]string{
	"ServerEdition": "diffed by the dedicated server_edition clauses (restart projection, admin_emails, access); a zero-value block projects the same bytes as nil",
	"TLS":           "diffed (restart-gated) by the dedicated tls clause; the baseline here already carries a TLS block, so a zero-value edit is not a change",
}

// mutateConfigField changes one top-level field of cfg to a value that differs
// from its zero value and reports whether it knew how.
func mutateConfigField(cfg *config.Config, f reflect.StructField, tryFalse bool) bool {
	v := reflect.ValueOf(cfg).Elem().FieldByName(f.Name)
	switch v.Kind() {
	case reflect.String:
		v.SetString("changed-value")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 7)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		if elem.Kind() == reflect.String {
			elem.SetString("changed-value")
		}
		v.Set(reflect.Append(v, elem))
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		switch p.Elem().Kind() {
		case reflect.Bool:
			p.Elem().SetBool(!tryFalse)
		case reflect.Int, reflect.Int64:
			p.Elem().SetInt(7)
		}
		v.Set(p)
	default:
		return false
	}
	return true
}

func TestDetectConfigChanges_EveryTopLevelFieldIsDiffed(t *testing.T) {
	typ := reflect.TypeOf(config.Config{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() || f.Tag.Get("json") == "-" {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			if reason, ok := configFieldsNotDiffed[f.Name]; ok {
				assert.NotEmpty(t, reason)
				t.Skipf("intentionally not diffed: %s", reason)
			}
			oldCfg := &config.Config{Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{}}
			edited := &config.Config{Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{}}
			if !mutateConfigField(edited, f, false) {
				t.Fatalf("test cannot mutate field %s (kind %s): extend mutateConfigField", f.Name, f.Type.Kind())
			}
			result := DetectConfigChanges(oldCfg, edited)
			if len(result.ChangedFields) == 0 && f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Bool {
				// A *bool whose unset state means "true": the edit that matters is false.
				edited = &config.Config{Listen: "127.0.0.1:8080", DataDir: "/d", TLS: &config.TLSConfig{}}
				mutateConfigField(edited, f, true)
				result = DetectConfigChanges(oldCfg, edited)
			}
			assert.NotEmpty(t, result.ChangedFields, "editing %s alone is reported as \"No configuration changes detected\"", f.Name)
		})
	}
}
