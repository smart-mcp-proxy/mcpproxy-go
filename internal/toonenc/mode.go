package toonenc

// Mode is the resolved TOON output mode for a tool call (global config value
// or per-server override, resolved by config.ResolveToonOutput and parsed
// here at the server/bench boundary).
type Mode string

const (
	// ModeOff disables the feature: the encoder is never invoked and
	// responses are byte-identical to pre-feature behavior (FR-002). Default.
	ModeOff Mode = "off"
	// ModeAdaptive encodes a block iff it is tabular-uniform (FR-003b) AND
	// the complete encoded emission beats the exact passthrough emission by
	// at least the configured threshold (FR-003c).
	ModeAdaptive Mode = "adaptive"
	// ModeAlways encodes every JSON-parseable text block regardless of
	// tabular classification or size comparison (benchmark/debug only,
	// FR-009). Non-JSON text still passes through unmarked.
	ModeAlways Mode = "always"
)

// ParseMode parses a config-level toon_output string into a Mode. The empty
// string maps to ModeOff (unset = default). Unknown values return ("", false);
// callers must treat that as off (config validation rejects such values
// before they reach a live call, so this is a defensive backstop).
func ParseMode(s string) (Mode, bool) {
	switch s {
	case "":
		return ModeOff, true
	case string(ModeOff):
		return ModeOff, true
	case string(ModeAdaptive):
		return ModeAdaptive, true
	case string(ModeAlways):
		return ModeAlways, true
	default:
		return "", false
	}
}
