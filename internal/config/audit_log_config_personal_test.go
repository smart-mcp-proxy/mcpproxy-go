//go:build !server

package config

import "testing"

// Spec 107 T108: the personal edition always resolves audit_log to disabled -
// audit_log is a server-edition BUILD feature (isServerEditionBuild), not
// gated by the server_edition.enabled config flag, so even an explicit block
// on the personal binary has nothing to bind to.
func TestEffectiveAuditLog_PersonalEdition_AlwaysDisabled(t *testing.T) {
	enabled := true
	stdout := true
	cfg := &Config{AuditLog: &AuditLogConfig{Enabled: &enabled, Stdout: &stdout}}

	for _, transport := range []string{TransportHTTP, TransportStdio} {
		resolved, warn, err := EffectiveAuditLog(cfg, transport)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", transport, err)
		}
		if warn != "" {
			t.Fatalf("[%s] unexpected warning: %q", transport, warn)
		}
		if resolved.Enabled {
			t.Fatalf("[%s] expected disabled on the personal edition, got %+v", transport, resolved)
		}
	}
}
