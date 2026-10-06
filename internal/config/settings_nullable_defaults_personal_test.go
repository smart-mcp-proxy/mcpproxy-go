//go:build !server

package config

import "testing"

// Spec 109 D35 (T161): the personal edition leaves audit logging off when the
// audit_log block is absent; the Settings toggles must agree.
func TestSettingsNullableDefaultsAbsentAuditLogBlock_Personal(t *testing.T) {
	fx := loadNullableDefaultsFixture(t)
	resolved, _, err := EffectiveAuditLog(&Config{}, TransportHTTP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := fixtureBlock(t, fx, "audit_log.enabled").AbsentBlock["personal"]; resolved.Enabled != want {
		t.Errorf("absent-block audit_log.enabled = %v, fixture says %v", resolved.Enabled, want)
	}
	if want := fixtureBlock(t, fx, "audit_log.stdout").AbsentBlock["personal"]; resolved.Stdout != want {
		t.Errorf("absent-block audit_log.stdout = %v, fixture says %v", resolved.Stdout, want)
	}
	if want := fixtureBlock(t, fx, "audit_log.compress").AbsentBlock["personal"]; resolved.Compress != want {
		t.Errorf("absent-block audit_log.compress = %v, fixture says %v", resolved.Compress, want)
	}
}
