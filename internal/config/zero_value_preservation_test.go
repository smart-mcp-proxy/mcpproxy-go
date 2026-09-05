package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1175: three config fields carried `omitempty` on a numeric type while 0 was
// a meaningful, documented value. SaveConfig is json.MarshalIndent, so an
// operator's explicit 0 was deleted from mcp_config.json on the next save and
// the field silently reverted to its default:
//
//	max_result_size_chars           0 -> 500000  ("Set to 0 to disable")
//	activity_max_size_mb            0 -> 256     ("0=disabled")
//	observability.tracing.sample_rate 0 -> 0.1   (sample nothing)
//
// The distinction that has to survive is three-valued, which is why dropping
// `omitempty` from the int is NOT equivalent: absent means "the default
// applies", an explicit 0 means "the operator disabled it", and both must
// round-trip through marshal -> decode without becoming the other.

func writeAndReload(t *testing.T, cfg *Config) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, SaveConfig(cfg, path))
	reloaded, err := LoadFromFile(path)
	require.NoError(t, err)
	return reloaded
}

func rawKeys(t *testing.T, cfg *Config) map[string]interface{} {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, SaveConfig(cfg, path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	return raw
}

func TestExplicitZeroMaxResultSizeCharsSurvivesSave(t *testing.T) {
	cfg := DefaultConfig()
	zero := 0
	cfg.MaxResultSizeChars = &zero

	raw := rawKeys(t, cfg)
	require.Contains(t, raw, "max_result_size_chars",
		"an operator's explicit 0 must be written, not erased by omitempty")
	assert.EqualValues(t, 0, raw["max_result_size_chars"])

	reloaded := writeAndReload(t, cfg)
	require.NotNil(t, reloaded.MaxResultSizeChars)
	assert.Equal(t, 0, *reloaded.MaxResultSizeChars)
	assert.Equal(t, 0, reloaded.EffectiveMaxResultSizeChars(),
		"0 means disabled and must stay disabled across a save")
}

func TestExplicitZeroActivityMaxSizeMBSurvivesSave(t *testing.T) {
	cfg := DefaultConfig()
	zero := 0
	cfg.ActivityMaxSizeMB = &zero

	reloaded := writeAndReload(t, cfg)
	require.NotNil(t, reloaded.ActivityMaxSizeMB)
	assert.Equal(t, 0, reloaded.EffectiveActivityMaxSizeMB(),
		"0 disables the activity size cap and must survive a save")
}

func TestExplicitZeroSampleRateSurvivesSave(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg.Observability)
	require.NotNil(t, cfg.Observability.Tracing)
	zero := 0.0
	cfg.Observability.Tracing.SampleRate = &zero

	reloaded := writeAndReload(t, cfg)
	require.NotNil(t, reloaded.Observability)
	require.NotNil(t, reloaded.Observability.Tracing)
	assert.Equal(t, 0.0, reloaded.Observability.Tracing.EffectiveSampleRate(),
		"an operator who turns sampling off must not get 10% back")
}

// The mirror image, and the reason dropping omitempty is not the fix: a config
// that never mentioned the key must stay silent about it and keep the default.
func TestAbsentKeysStayAbsentAndKeepDefaults(t *testing.T) {
	cfg := DefaultConfig()
	require.Nil(t, cfg.MaxResultSizeChars, "the default config states defaults in the reader, not the field")
	require.Nil(t, cfg.ActivityMaxSizeMB)

	raw := rawKeys(t, cfg)
	assert.NotContains(t, raw, "max_result_size_chars",
		"an unset key must not be materialised at its default")
	assert.NotContains(t, raw, "activity_max_size_mb")

	reloaded := writeAndReload(t, cfg)
	assert.Nil(t, reloaded.MaxResultSizeChars)
	assert.Equal(t, defaultMaxResultSizeChars, reloaded.EffectiveMaxResultSizeChars())
	assert.Equal(t, defaultActivityMaxSizeMB, reloaded.EffectiveActivityMaxSizeMB())
	assert.Equal(t, defaultTracingSampleRate, reloaded.Observability.Tracing.EffectiveSampleRate())
}

// Two REST write paths decode into a ZERO config.Config rather than a
// DefaultConfig-seeded one (handlePatchConfig's `var merged config.Config`,
// and oauth.UnmaskLiveConfigDocument's `&config.Config{}`). Any default that
// lives only in DefaultConfig() is therefore wrong on the API path, which is
// why the Effective readers — not the constructor — are the resolution point.
func TestEffectiveReadersResolveOnAZeroValuedConfig(t *testing.T) {
	var cfg Config
	assert.Equal(t, defaultMaxResultSizeChars, cfg.EffectiveMaxResultSizeChars())
	assert.Equal(t, defaultActivityMaxSizeMB, cfg.EffectiveActivityMaxSizeMB())

	var tracing TracingExporterConfig
	assert.Equal(t, defaultTracingSampleRate, tracing.EffectiveSampleRate())

	var nilTracing *TracingExporterConfig
	assert.Equal(t, defaultTracingSampleRate, nilTracing.EffectiveSampleRate(),
		"the reader must be nil-safe: an absent tracing block is the common case")
}

// numericOmitemptyAllowlist records every numeric config field that keeps
// `omitempty` deliberately, with the reason it is safe. A field is safe only
// when 0 and absent mean the SAME thing — otherwise the value an operator
// wrote is deleted on the next SaveConfig, which is exactly #1175.
//
// Adding a field here is a decision, not a formality: say why 0 is not a
// meaningful value, or convert the field to a pointer instead.
var numericOmitemptyAllowlist = map[string]string{
	// "0 = use the default" by construction: the setters ignore non-positive
	// values so the store can never be unbounded again (#1176).
	"Config.ToolCallMaxResponseSize":     "non-positive means 'use the default'; there is deliberately no off switch",
	"Config.ToolCallMaxRecordsPerServer": "non-positive means 'use the default'; there is deliberately no off switch",
	"Config.ActivityMaxResponseSize":     "ActivityService.SetMaxResponseSize ignores non-positive, so 0 and absent both mean 64KB",
	"Config.ActivityRetentionDays":       "EffectiveActivityRetentionDays resolves <= 0 to the same 90 days an absent key gets",
	"Config.ActivityMaxRecords":          "EffectiveActivityMaxRecords resolves <= 0 to the same 100000 an absent key gets",
	"Config.ActivityCleanupIntervalMin":  "0 and absent both fall back to the built-in interval",
	"Config.TopK":                        "deprecated and inert; superseded by ToolsLimit",
	"Config.OAuthExpiryWarningHours":     "0 and absent both fall back to the 1.0h default",
	"Config.ToonMinSavingsPct":           "documented as '0/unset -> 15' — the two are the same value by design",

	// Durations whose own doc comments say zero and unset are the same value.
	"Config.Servers.LauncherWaitTimeout": "doc comment: 'Zero or unset -> 30s default'",
	// The one entry whose justification is "unobservable" rather than "equal":
	// this field has exactly one reader, a diagnostics display
	// (internal/server/mcp.go:4033 renders it in a doctor payload). It is never
	// used as an actual timeout, so an erased 0 changes a reported number and
	// nothing else. Give it a real reader and it must be converted.
	"Config.DockerIsolation.Timeout":            "no functional reader; sole use is the doctor payload at internal/server/mcp.go:4033",
	"Config.Observability.UsageCacheTTL":        "doc comment: 'Default 5s' — 0 and absent both resolve to it",
	"Config.Observability.UsagePersistInterval": "doc comment: 'Default 30s' — 0 and absent both resolve to it",
	"Config.Security.ScanTimeoutDefault":        "0 and absent both take the built-in scan timeout",
	"Config.Security.IntegrityCheckInterval":    "0 and absent both take the built-in check interval",

	// Code execution: every one of these is validated as "0 means use default"
	// (Validate: 'must be between 1 and 600000 milliseconds (or 0 for default)').
	"Config.CodeExecutionTimeoutMs":    "validated as '0 for default'",
	"Config.CodeExecutionPoolSize":     "validated as '0 for default'",
	"Config.CodeExecutionMaxParallel":  "validated as '0 for default'",
	"Config.CodeExecutionMaxToolCalls": "0 means unlimited AND is the default, so erasing it is a no-op",

	// 0 is not distinguishable from absent because the reader treats <= 0 as
	// "use the default" — the same shape as an Effective…() pointer reader,
	// minus the pointer.
	"Config.OutputValidation.MaxBytes": "EffectiveMaxBytes treats <= 0 as the 5MB default",
	"Config.OutputValidation.MaxDepth": "EffectiveMaxDepth treats <= 0 as the depth-64 default",

	// Server edition (//go:build server). These are invisible to an untagged
	// run of this test, which is why it must also be run as
	// `go test -tags server ./internal/config`. All four are normalised by
	// ServerEditionConfig.Validate, which treats <= 0 as "use the default"
	// (internal/config/server_edition_config.go:90-101).
	"Config.ServerEdition.SessionTTL":           "Validate: <= 0 becomes the 24h default",
	"Config.ServerEdition.BearerTokenTTL":       "Validate: <= 0 becomes the 24h default",
	"Config.ServerEdition.WorkspaceIdleTimeout": "Validate: <= 0 becomes the 30m default",
	"Config.ServerEdition.MaxUserServers":       "Validate: <= 0 becomes the 20 default",

	// 0 and absent both fall back to the DefaultConfig value; there is no
	// documented meaning for an explicit 0.
	"Config.DockerRecovery.MaxRetries":        "0 means unlimited AND is the zero default, so erasing it is a no-op",
	"Config.OutputSanitisation.MaxRedactions": "no production reader distinguishes 0 from absent; both take the 100 default",
}

// TestNumericFieldsWithOmitemptyAreAllowlisted walks the Config tree and fails
// on any numeric field carrying `omitempty` that has not been declared safe.
// This is the guard that stops the class coming back a fourth time.
func TestNumericFieldsWithOmitemptyAreAllowlisted(t *testing.T) {
	var offenders []string
	seen := map[reflect.Type]bool{}

	var walk func(t reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true

		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.PkgPath != "" { // unexported
				continue
			}
			tag := field.Tag.Get("json")
			name := path + "." + field.Name

			if isNumericKind(field.Type.Kind()) && strings.Contains(tag, ",omitempty") {
				if _, ok := numericOmitemptyAllowlist[name]; !ok {
					offenders = append(offenders, name+" (json:"+strings.Split(tag, ",")[0]+")")
				}
			}
			walk(field.Type, name)
		}
	}
	walk(reflect.TypeOf(Config{}), "Config")

	assert.Empty(t, offenders,
		"numeric fields with omitempty silently erase an explicitly configured 0 on SaveConfig (#1175).\n"+
			"Either make the field a pointer with an Effective…() reader, or add it to numericOmitemptyAllowlist\n"+
			"with the reason 0 and absent mean the same thing.\nOffenders:\n  %s",
		strings.Join(offenders, "\n  "))
}

func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// A cross-model review caught two allowlist entries whose stated reason was
// false: an explicit 0 on these did NOT mean the same as an absent key. An
// omitted key took DefaultConfig's 90 days / 100000 records, while an explicit
// 0 was ignored by ActivityService.SetRetentionConfig and fell through to that
// service's own far smaller constants (7 days / 10000). Saving the config then
// erased the 0 and flipped the operator back — the #1175 defect in a quieter
// form, and a tenfold change in how much history is kept.
func TestZeroAndAbsentRetentionCountsResolveTheSame(t *testing.T) {
	absent := DefaultConfig()
	absent.ActivityRetentionDays = 0
	absent.ActivityMaxRecords = 0

	assert.Equal(t, defaultActivityRetentionDay, absent.EffectiveActivityRetentionDays(),
		"an explicit 0 must resolve to the documented default, not to a smaller service constant")
	assert.Equal(t, defaultActivityMaxRecords, absent.EffectiveActivityMaxRecords())

	var zero Config
	assert.Equal(t, defaultActivityRetentionDay, zero.EffectiveActivityRetentionDays(),
		"the REST write paths decode into a zero Config and must resolve the same way")
	assert.Equal(t, defaultActivityMaxRecords, zero.EffectiveActivityMaxRecords())

	set := DefaultConfig()
	set.ActivityRetentionDays = 3
	set.ActivityMaxRecords = 42
	assert.Equal(t, 3, set.EffectiveActivityRetentionDays(), "a real value is passed through")
	assert.Equal(t, 42, set.EffectiveActivityMaxRecords())
}
