// Package diagnostics implements the stable error-code catalog used by mcpproxy
// to surface human-readable, fixable failure states to the user.
//
// Every terminal (non-automatically-recoverable) error in the upstream, OAuth,
// Docker, config, and network paths is classified into a stable code of the
// form MCPX_<DOMAIN>_<SPECIFIC>. Once shipped, a code is never renamed;
// deprecation is the only allowed transition. See spec 044.
package diagnostics

import "time"

// Code is a stable error-code identifier. Immutable across releases once shipped.
type Code string

// Severity drives UI badge colour and CLI formatting.
type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// FixStepType tells the UI how to render a fix step.
type FixStepType string

const (
	FixStepLink    FixStepType = "link"    // external URL; clickable
	FixStepCommand FixStepType = "command" // shell command; copyable
	FixStepButton  FixStepType = "button"  // triggers a registered fixer via /api/v1/diagnostics/fix
)

// FixStep is one actionable remediation suggestion attached to a CatalogEntry.
type FixStep struct {
	Type        FixStepType `json:"type"`
	Label       string      `json:"label"`
	Command     string      `json:"command,omitempty"`
	URL         string      `json:"url,omitempty"`
	FixerKey    string      `json:"fixer_key,omitempty"`
	Destructive bool        `json:"destructive,omitempty"`
}

// CatalogEntry is the authoritative description of one error code.
type CatalogEntry struct {
	Code        Code      `json:"code"`
	Severity    Severity  `json:"severity"`
	UserMessage string    `json:"user_message"`
	FixSteps    []FixStep `json:"fix_steps"`
	DocsURL     string    `json:"docs_url"`
	Deprecated  bool      `json:"deprecated,omitempty"`
	ReplacedBy  Code      `json:"replaced_by,omitempty"`
}

// DiagnosticError is the runtime record attached to a server's stateview snapshot
// while the server has an active failure.
type DiagnosticError struct {
	Code       Code      `json:"code"`
	Severity   Severity  `json:"severity"`
	Cause      string    `json:"cause,omitempty"`
	CauseType  string    `json:"cause_type,omitempty"`
	ServerID   string    `json:"server_id"`
	DetectedAt time.Time `json:"detected_at"`
}

// ClassifierHints lets callers nudge the classifier when context is known
// (e.g., "this error came from the stdio spawn path").
type ClassifierHints struct {
	Transport string // "stdio", "http", "sse", "docker", etc.
	ServerID  string
}

// FixRequest is the input to a registered fixer.
type FixRequest struct {
	ServerID string
	Mode     string // "dry_run" or "execute"
}

// FixResult is the output of a registered fixer.
type FixResult struct {
	Outcome    string // "success" | "failed" | "blocked"
	Preview    string // populated for dry_run
	FailureMsg string
}

// Outcome constants used in FixResult.Outcome.
const (
	OutcomeSuccess = "success"
	OutcomeFailed  = "failed"
	OutcomeBlocked = "blocked"
)

// Mode constants used in FixRequest.Mode.
const (
	ModeDryRun  = "dry_run"
	ModeExecute = "execute"
)
