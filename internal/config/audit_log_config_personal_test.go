//go:build !server

package config

import "testing"

// Spec 107 T108 (round-2 cross-review fix, PR-D): only the per-edition
// DEFAULT for an ABSENT audit_log block differs — FR-014 "Defaults differ
// by edition, the code does not" / "An explicit value always wins" is not a
// server-edition-only promise. EffectiveAuditLog used to hard-gate the
// entire function behind isServerEditionBuild, so an explicit
// `audit_log: {enabled: true, ...}` on the personal binary silently
// resolved to {Enabled:false} — this pins the corrected behavior instead.

func TestEffectiveAuditLog_PersonalEdition_AbsentBlockDisabledNoWarning(t *testing.T) {
	cfg := &Config{}

	for _, transport := range []string{TransportHTTP, TransportStdio} {
		resolved, warn, err := EffectiveAuditLog(cfg, transport)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", transport, err)
		}
		if warn != "" {
			t.Fatalf("[%s] unexpected warning for the absent-block personal default: %q", transport, warn)
		}
		if resolved.Enabled {
			t.Fatalf("[%s] expected the personal-edition absent-block default to be disabled, got %+v", transport, resolved)
		}
	}
}

func TestEffectiveAuditLog_PersonalEdition_ExplicitEnabledIsHonoured(t *testing.T) {
	enabled := true
	stdout := true
	cfg := &Config{AuditLog: &AuditLogConfig{Enabled: &enabled, Stdout: &stdout}}

	resolved, warn, err := EffectiveAuditLog(cfg, TransportHTTP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if !resolved.Enabled || !resolved.Stdout {
		t.Fatalf("an explicit audit_log.enabled:true, stdout:true on the personal edition must be honoured, got %+v", resolved)
	}
}

// TestEffectiveAuditLog_PersonalEdition_ExplicitDisabledIsHonoured proves the
// personal edition's DEFAULT (disabled, no warning) is not confused with an
// explicit `enabled: false`, which — like the server edition — must still
// warn that attribution is off, not stay silent.
func TestEffectiveAuditLog_PersonalEdition_ExplicitDisabledWarns(t *testing.T) {
	enabled := false
	cfg := &Config{AuditLog: &AuditLogConfig{Enabled: &enabled}}

	resolved, warn, err := EffectiveAuditLog(cfg, TransportHTTP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != MsgAuditLogDisabledNotice {
		t.Fatalf("warning = %q, want %q", warn, MsgAuditLogDisabledNotice)
	}
	if resolved.Enabled {
		t.Fatalf("expected disabled, got %+v", resolved)
	}
}

// TestEffectiveAuditLog_PersonalEdition_ExplicitStdoutNoPath_Stdio_StartupError
// proves the stdio-transport rule (FR-014) is edition-neutral: an explicit
// enabled+stdout with no path is refused with exit code 4 on the personal
// binary exactly as it is on the server binary, never silently disabled.
func TestEffectiveAuditLog_PersonalEdition_ExplicitStdoutNoPath_Stdio_StartupError(t *testing.T) {
	enabled := true
	stdout := true
	cfg := &Config{AuditLog: &AuditLogConfig{Enabled: &enabled, Stdout: &stdout}}

	resolved, warn, err := EffectiveAuditLog(cfg, TransportStdio)
	if err == nil {
		t.Fatalf("expected a StartupError, got resolved=%+v warn=%q", resolved, warn)
	}
	startupErr, ok := err.(*StartupError)
	if !ok {
		t.Fatalf("error is not *StartupError: %T: %v", err, err)
	}
	if startupErr.ExitCode != ExitCodeAuditLogError {
		t.Fatalf("ExitCode = %d, want %d", startupErr.ExitCode, ExitCodeAuditLogError)
	}
	if startupErr.Message != MsgAuditLogStdoutRefusedStdio {
		t.Fatalf("Message = %q, want %q", startupErr.Message, MsgAuditLogStdoutRefusedStdio)
	}
}
