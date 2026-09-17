//go:build server

package config

import (
	"encoding/json"
	"testing"
)

func TestEffectiveAuditLog_AbsentBlock_ServerEdition(t *testing.T) {
	cfg := &Config{}

	t.Run("http default is stdout:true", func(t *testing.T) {
		resolved, warn, err := EffectiveAuditLog(cfg, TransportHTTP)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if warn != "" {
			t.Fatalf("unexpected warning: %q", warn)
		}
		if !resolved.Enabled || !resolved.Stdout {
			t.Fatalf("expected {Enabled:true, Stdout:true}, got %+v", resolved)
		}
	})

	t.Run("stdio default is disabled with WARN naming audit_log.path", func(t *testing.T) {
		resolved, warn, err := EffectiveAuditLog(cfg, TransportStdio)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if warn != MsgAuditLogStdoutIgnoredStdio {
			t.Fatalf("warning = %q, want %q", warn, MsgAuditLogStdoutIgnoredStdio)
		}
		if resolved.Enabled {
			t.Fatalf("expected disabled sink under stdio, got %+v", resolved)
		}
	})
}

func TestEffectiveAuditLog_ExplicitStdoutNoPath_Stdio_StartupError(t *testing.T) {
	cfg := &Config{AuditLog: &AuditLogConfig{
		Enabled: boolPtr(true),
		Stdout:  boolPtr(true),
	}}
	resolved, warn, err := EffectiveAuditLog(cfg, TransportStdio)
	if err == nil {
		t.Fatalf("expected a StartupError, got resolved=%+v warn=%q", resolved, warn)
	}
	var startupErr *StartupError
	if se, ok := err.(*StartupError); ok {
		startupErr = se
	} else {
		t.Fatalf("error is not *StartupError: %T: %v", err, err)
	}
	if startupErr.ExitCode != ExitCodeAuditLogError {
		t.Fatalf("ExitCode = %d, want %d", startupErr.ExitCode, ExitCodeAuditLogError)
	}
	if startupErr.Message != MsgAuditLogStdoutRefusedStdio {
		t.Fatalf("Message = %q, want %q", startupErr.Message, MsgAuditLogStdoutRefusedStdio)
	}
}

func TestEffectiveAuditLog_ExplicitPath_Stdio_Honoured(t *testing.T) {
	cfg := &Config{AuditLog: &AuditLogConfig{
		Enabled: boolPtr(true),
		Path:    "/tmp/audit.jsonl",
		Stdout:  boolPtr(true), // dropped with a WARN, never refused
	}}
	resolved, warn, err := EffectiveAuditLog(cfg, TransportStdio)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warn != MsgAuditLogStdoutIgnoredStdio {
		t.Fatalf("warning = %q, want %q", warn, MsgAuditLogStdoutIgnoredStdio)
	}
	if !resolved.Enabled || resolved.Stdout || resolved.Path != "/tmp/audit.jsonl" {
		t.Fatalf("resolved = %+v, want enabled file sink with stdout dropped", resolved)
	}
}

func TestEffectiveAuditLog_ExplicitPath_HTTP_Honoured(t *testing.T) {
	cfg := &Config{AuditLog: &AuditLogConfig{
		Path: "/tmp/audit.jsonl",
	}}
	resolved, warn, err := EffectiveAuditLog(cfg, TransportHTTP)
	if err != nil || warn != "" {
		t.Fatalf("unexpected warn/err: %q / %v", warn, err)
	}
	if !resolved.Enabled || resolved.Stdout || resolved.Path != "/tmp/audit.jsonl" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestEffectiveAuditLog_OmittedVsExplicit_Fields(t *testing.T) {
	t.Run("compress omitted defaults true, explicit false wins", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Path: "/tmp/a.jsonl"}}
		resolved, _, err := EffectiveAuditLog(cfg, TransportHTTP)
		if err != nil {
			t.Fatal(err)
		}
		if !resolved.Compress {
			t.Fatalf("expected default Compress=true, got %+v", resolved)
		}

		cfg.AuditLog.Compress = boolPtr(false)
		resolved, _, err = EffectiveAuditLog(cfg, TransportHTTP)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Compress {
			t.Fatalf("expected explicit Compress=false to win, got %+v", resolved)
		}
	})

	t.Run("max_size_mb omitted defaults 50, explicit 0 is a validation error not a silent zero", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Path: "/tmp/a.jsonl"}}
		resolved, _, err := EffectiveAuditLog(cfg, TransportHTTP)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.MaxSizeMB != DefaultAuditLogMaxSizeMB {
			t.Fatalf("MaxSizeMB = %d, want %d", resolved.MaxSizeMB, DefaultAuditLogMaxSizeMB)
		}

		cfg.AuditLog.MaxSizeMB = intPtr(0)
		errs := validateAuditLog(cfg)
		if len(errs) == 0 {
			t.Fatalf("expected a validation error for max_size_mb: 0")
		}
	})
}

func TestValidateAuditLog(t *testing.T) {
	t.Run("enabled with neither stdout nor path is refused", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(true)}}
		errs := validateAuditLog(cfg)
		if len(errs) != 1 || errs[0].Message != MsgAuditLogNoSink {
			t.Fatalf("errs = %+v, want one %q", errs, MsgAuditLogNoSink)
		}
	})

	t.Run("enabled with stdout only is fine", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(true), Stdout: boolPtr(true)}}
		if errs := validateAuditLog(cfg); len(errs) != 0 {
			t.Fatalf("unexpected errors: %+v", errs)
		}
	})

	t.Run("non-positive rotation values with a path are refused", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(true), Path: "/tmp/a.jsonl", MaxBackups: intPtr(0)}}
		errs := validateAuditLog(cfg)
		if len(errs) != 1 || errs[0].Field != "audit_log.max_backups" {
			t.Fatalf("errs = %+v", errs)
		}
	})

	t.Run("disabled block has no rule regardless of shape", func(t *testing.T) {
		cfg := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(false)}}
		if errs := validateAuditLog(cfg); len(errs) != 0 {
			t.Fatalf("unexpected errors: %+v", errs)
		}
	})

	t.Run("reached from Config.Validate and ValidateDetailed", func(t *testing.T) {
		cfg := &Config{Listen: "127.0.0.1:0", AuditLog: &AuditLogConfig{Enabled: boolPtr(true)}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected Config.Validate to refuse a sinkless enabled block")
		}
		cfg2 := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(true)}}
		found := false
		for _, e := range cfg2.ValidateDetailed() {
			if e.Field == "audit_log" {
				found = true
			}
		}
		if !found {
			t.Fatalf("ValidateDetailed did not report audit_log")
		}
	})
}

func TestAuditLogEnvOverrides(t *testing.T) {
	t.Setenv("MCPPROXY_AUDIT_LOG_ENABLED", "true")
	t.Setenv("MCPPROXY_AUDIT_LOG_PATH", "/var/log/audit.jsonl")
	t.Setenv("MCPPROXY_AUDIT_LOG_STDOUT", "false")

	cfg := &Config{}
	applyTLSEnvOverrides(cfg)

	if cfg.AuditLog == nil {
		t.Fatalf("expected env overrides to materialize audit_log")
	}
	if cfg.AuditLog.Enabled == nil || !*cfg.AuditLog.Enabled {
		t.Fatalf("Enabled = %v, want true", cfg.AuditLog.Enabled)
	}
	if cfg.AuditLog.Path != "/var/log/audit.jsonl" {
		t.Fatalf("Path = %q", cfg.AuditLog.Path)
	}
	if cfg.AuditLog.Stdout == nil || *cfg.AuditLog.Stdout {
		t.Fatalf("Stdout = %v, want false", cfg.AuditLog.Stdout)
	}
}

func TestAuditLogConfig_MarshalsDifferently(t *testing.T) {
	// The restart-pinned DetectConfigChanges clause (internal/runtime, which
	// imports this package - so it is exercised there, not here) compares
	// cfg.AuditLog with jsonEqual; this pins that the two blocks below do NOT
	// marshal identically, which is what makes that clause fire.
	old := &Config{}
	newCfg := &Config{AuditLog: &AuditLogConfig{Enabled: boolPtr(true), Stdout: boolPtr(true)}}

	oldJSON, err := json.Marshal(old.AuditLog)
	if err != nil {
		t.Fatal(err)
	}
	newJSON, err := json.Marshal(newCfg.AuditLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldJSON) == string(newJSON) {
		t.Fatalf("expected the two audit_log blocks to marshal differently")
	}
}
