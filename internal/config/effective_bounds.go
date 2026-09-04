package config

// Defaults for the tri-state numeric settings of #1175.
//
// They live here, next to the readers that apply them, rather than only in
// DefaultConfig(): two REST write paths decode a client document into a
// ZERO-valued Config — handlePatchConfig's `var merged config.Config` and
// oauth.UnmaskLiveConfigDocument's `&config.Config{}` — and never see the
// constructor. A default that lives only in DefaultConfig() is wrong on those
// paths, so the reader is the single resolution point.
const (
	defaultMaxResultSizeChars = 500000 // Claude Code's inline-response hard max
	defaultActivityMaxSizeMB  = 256    // total activity-log size cap in MB
)

// EffectiveMaxResultSizeChars resolves the `_meta.anthropic/maxResultSizeChars`
// ceiling advertised on every tool.
//
// nil (key absent) = the built-in default. An explicit 0 = the operator
// disabled the annotation, and stays 0.
func (c *Config) EffectiveMaxResultSizeChars() int {
	if c == nil || c.MaxResultSizeChars == nil {
		return defaultMaxResultSizeChars
	}
	return *c.MaxResultSizeChars
}

// EffectiveActivityMaxSizeMB resolves the total activity-log size cap in MB.
//
// nil (key absent) = the built-in default. An explicit 0 = the size cap is
// disabled, and stays disabled.
func (c *Config) EffectiveActivityMaxSizeMB() int {
	if c == nil || c.ActivityMaxSizeMB == nil {
		return defaultActivityMaxSizeMB
	}
	return *c.ActivityMaxSizeMB
}

// EffectiveSampleRate resolves the head-based trace sampling ratio.
//
// nil (key absent) = the built-in 0.1. An explicit 0 = sample nothing, and
// stays nothing. Nil-safe on the receiver: an absent tracing block is the
// common case.
func (t *TracingExporterConfig) EffectiveSampleRate() float64 {
	if t == nil || t.SampleRate == nil {
		return defaultTracingSampleRate
	}
	return *t.SampleRate
}
