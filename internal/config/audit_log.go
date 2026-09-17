package config

// audit_log.go: config surface for the Spec 107 edition-neutral audit sink
// (internal/audit). Contracts: contracts/config-keys.md `audit_log*` rows,
// data-model.md §9, spec.md FR-012..FR-019.

// Transport values EffectiveAuditLog accepts (Spec 107 FR-014): "http" for
// the HTTP/SSE listener, "stdio" for the native stdio MCP transport (where
// stdout is the JSON-RPC channel and can never double as a log sink).
const (
	TransportHTTP  = "http"
	TransportStdio = "stdio"
)

// Default rotation values (contracts/config-keys.md).
const (
	DefaultAuditLogMaxSizeMB  = 50
	DefaultAuditLogMaxBackups = 10
	DefaultAuditLogMaxAgeDays = 90
)

// Boot/doctor/validation message text (contracts/config-keys.md, exact
// strings — both the log line and the write-door validators use these).
const (
	// MsgAuditLogStdoutIgnoredStdio is the WARN line for a stdout sink
	// silently suppressed under the stdio transport: an absent audit_log
	// block whose per-edition default would otherwise be stdout:true, or an
	// explicit block whose `stdout` was left unset (never for an explicit
	// `stdout: true`, which is refused instead — see
	// MsgAuditLogStdoutRefusedStdio).
	MsgAuditLogStdoutIgnoredStdio = "audit_log.stdout is ignored under the stdio transport; set audit_log.path"

	// MsgAuditLogStdoutRefusedStdio is the StartupError message for an
	// explicit `enabled: true, stdout: true` with no `path` under the stdio
	// transport (FR-014: an explicit value always wins - it is refused,
	// never silently disabled).
	MsgAuditLogStdoutRefusedStdio = "audit_log.stdout cannot be used under the stdio transport (stdout carries JSON-RPC); set audit_log.path"

	// MsgAuditLogDisabledNotice is the startup notice for an explicit
	// audit_log.enabled: false under the server edition.
	MsgAuditLogDisabledNotice = "audit attribution is off"

	// MsgAuditLogNoSink is the validation message for enabled:true with
	// neither stdout nor a path set.
	MsgAuditLogNoSink = "audit_log is enabled but has no sink (set stdout: true or a path)"
)

// AuditLogConfig is the wire shape of the `audit_log` key. Every field except
// Path is a pointer so an omitted key is distinguishable from an explicit
// zero/false value (data-model.md §9): omitted `compress` -> true default,
// explicit `false` -> false; omitted `max_size_mb` -> 50, explicit `0` -> a
// validation error. Path's own emptiness already is the "unset" signal, so it
// stays a plain string.
type AuditLogConfig struct {
	Enabled    *bool  `json:"enabled,omitempty" mapstructure:"enabled"`
	Path       string `json:"path,omitempty" mapstructure:"path"`
	Stdout     *bool  `json:"stdout,omitempty" mapstructure:"stdout"`
	MaxSizeMB  *int   `json:"max_size_mb,omitempty" mapstructure:"max-size-mb"`
	MaxBackups *int   `json:"max_backups,omitempty" mapstructure:"max-backups"`
	MaxAgeDays *int   `json:"max_age_days,omitempty" mapstructure:"max-age-days"`
	Compress   *bool  `json:"compress,omitempty" mapstructure:"compress"`
}

// ResolvedAuditLog is the plain, fully-defaulted form EffectiveAuditLog
// returns: every field carries a concrete value, ready for the sink
// constructors (internal/audit.NewFileSink / NewStdoutSink).
type ResolvedAuditLog struct {
	Enabled    bool
	Path       string
	Stdout     bool
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

// StartupError is a typed, exit-code-carrying boot failure (Spec 107 FR-014).
// cmd/mcpproxy's classifyError matches it with errors.As BEFORE its string
// heuristics, so a wrapped "permission denied" underneath never misclassifies
// this as ExitCodePermissionError.
type StartupError struct {
	ExitCode int
	Message  string
}

func (e *StartupError) Error() string { return e.Message }

// NewStartupError builds a StartupError with the given exit code and message.
func NewStartupError(exitCode int, message string) *StartupError {
	return &StartupError{ExitCode: exitCode, Message: message}
}

// EffectiveAuditLog resolves cfg.AuditLog into a plain ResolvedAuditLog for
// the given transport. It never mutates cfg.
//
// Return contract:
//   - (resolved, "", nil): use resolved as-is.
//   - (resolved, warning, nil): a default was silently adjusted for the
//     transport; the caller logs warning at WARN and still uses resolved
//     (which has Enabled=false in that case - FR-014 "only the default is
//     suppressed").
//   - (ResolvedAuditLog{}, "", err): an explicit configuration cannot be
//     honoured on this transport; err is always a *StartupError. The caller
//     must not construct a sink.
//
// The personal edition always resolves to {Enabled:false}: audit_log is a
// server-edition-build feature, keyed on the build tag (isServerEditionBuild),
// not on the server_edition.enabled feature flag - the audit funnels compile
// into every server-edition binary regardless of whether SSO is turned on.
func EffectiveAuditLog(cfg *Config, transport string) (resolved ResolvedAuditLog, warning string, err error) {
	resolved = ResolvedAuditLog{
		MaxSizeMB:  DefaultAuditLogMaxSizeMB,
		MaxBackups: DefaultAuditLogMaxBackups,
		MaxAgeDays: DefaultAuditLogMaxAgeDays,
		Compress:   true,
	}

	if !isServerEditionBuild {
		return resolved, "", nil
	}

	var block *AuditLogConfig
	if cfg != nil {
		block = cfg.AuditLog
	}

	if block == nil {
		// Per-edition default for an absent block: stdout:true on HTTP.
		if transport == TransportStdio {
			// Only the default is suppressed (FR-014); an explicit value
			// below always wins instead of being silently disabled.
			return resolved, MsgAuditLogStdoutIgnoredStdio, nil
		}
		resolved.Enabled = true
		resolved.Stdout = true
		return resolved, "", nil
	}

	stdoutExplicit := block.Stdout != nil

	if block.Enabled != nil {
		resolved.Enabled = *block.Enabled
	} else {
		resolved.Enabled = true
	}
	resolved.Path = block.Path
	if stdoutExplicit {
		resolved.Stdout = *block.Stdout
	}
	// Note: unlike the absent-block default above, an EXPLICIT block with
	// neither stdout nor path set is never silently defaulted to stdout:true
	// - it is refused by validateAuditLog at config-validate time
	// (contracts/config-keys.md: "enabled with neither stdout nor path").
	if block.MaxSizeMB != nil {
		resolved.MaxSizeMB = *block.MaxSizeMB
	}
	if block.MaxBackups != nil {
		resolved.MaxBackups = *block.MaxBackups
	}
	if block.MaxAgeDays != nil {
		resolved.MaxAgeDays = *block.MaxAgeDays
	}
	if block.Compress != nil {
		resolved.Compress = *block.Compress
	}

	if !resolved.Enabled {
		return resolved, "", nil
	}

	if transport == TransportStdio {
		switch {
		case resolved.Path != "":
			// An explicit path is honoured; a stdout:true beside it is
			// dropped with the WARN rather than refused.
			if resolved.Stdout {
				resolved.Stdout = false
				return resolved, MsgAuditLogStdoutIgnoredStdio, nil
			}
			return resolved, "", nil
		case stdoutExplicit && resolved.Stdout:
			// Explicit enabled+stdout with no path: refuse (FR-014).
			return ResolvedAuditLog{}, "", NewStartupError(ExitCodeAuditLogError, MsgAuditLogStdoutRefusedStdio)
		default:
			// stdout was only defaulted true (or is explicitly false) with no
			// path: suppress like the absent-block case rather than
			// constructing a no-op sink silently.
			resolved.Enabled = false
			resolved.Stdout = false
			return resolved, MsgAuditLogStdoutIgnoredStdio, nil
		}
	}

	return resolved, "", nil
}

// ExitCodeAuditLogError is the exit code an unhonourable audit_log
// configuration returns (Spec 107 FR-014: same band as every other
// configuration boot failure).
const ExitCodeAuditLogError = 4

// validateAuditLog is reached from both Config.Validate() and
// ValidateDetailed() (boot, PATCH, /config/apply agree - contracts/config-keys
// .md). It validates the wire block only; the stdio-transport rule lives in
// EffectiveAuditLog because it needs the transport, which is not known at
// validation time.
func validateAuditLog(cfg *Config) []ValidationError {
	if cfg == nil || cfg.AuditLog == nil {
		return nil
	}
	b := cfg.AuditLog
	// An explicit block with `enabled` omitted defaults to enabled - same as
	// EffectiveAuditLog's resolution for a present-but-unmarked block.
	enabled := b.Enabled == nil || *b.Enabled
	if !enabled {
		return nil
	}
	var errs []ValidationError
	hasStdout := b.Stdout != nil && *b.Stdout
	hasPath := b.Path != ""
	// Unlike an ABSENT block (which defaults to stdout:true on the server
	// edition), an explicit block with neither stdout nor path is refused
	// outright rather than silently defaulted.
	if !hasStdout && !hasPath {
		errs = append(errs, ValidationError{Field: "audit_log", Message: MsgAuditLogNoSink})
	}
	// Rotation values only matter for a file sink (contracts/config-keys.md:
	// "> 0 when a path is set").
	if hasPath {
		if b.MaxSizeMB != nil && *b.MaxSizeMB <= 0 {
			errs = append(errs, ValidationError{Field: "audit_log.max_size_mb", Message: "audit_log.max_size_mb must be positive"})
		}
		if b.MaxBackups != nil && *b.MaxBackups <= 0 {
			errs = append(errs, ValidationError{Field: "audit_log.max_backups", Message: "audit_log.max_backups must be positive"})
		}
		if b.MaxAgeDays != nil && *b.MaxAgeDays <= 0 {
			errs = append(errs, ValidationError{Field: "audit_log.max_age_days", Message: "audit_log.max_age_days must be positive"})
		}
	}
	return errs
}
