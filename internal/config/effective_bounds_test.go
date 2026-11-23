package config

import "testing"

// Spec 124 FR-031/FR-070: the work-session retention window defaults to 7
// days through its reader, on a zero-valued Config as well, and the record cap
// default is 400,000 while an explicit value is kept.
func TestEffectiveSessionRetentionAndRecordCap(t *testing.T) {
	var zero Config
	if got := zero.EffectiveSessionRetentionDays(); got != 7 {
		t.Errorf("zero config: session retention = %d, want 7", got)
	}
	if got := (*Config)(nil).EffectiveSessionRetentionDays(); got != 7 {
		t.Errorf("nil config: session retention = %d, want 7", got)
	}
	if got := (&Config{SessionRetentionDays: 14}).EffectiveSessionRetentionDays(); got != 14 {
		t.Errorf("explicit 14 = %d", got)
	}
	if got := (&Config{SessionRetentionDays: -1}).EffectiveSessionRetentionDays(); got != 7 {
		t.Errorf("negative = %d, want the default", got)
	}

	if got := zero.EffectiveActivityMaxRecords(); got != 400000 {
		t.Errorf("zero config: record cap = %d, want 400000", got)
	}
	if got := (&Config{ActivityMaxRecords: 100000}).EffectiveActivityMaxRecords(); got != 100000 {
		t.Errorf("an explicit 100000 must be kept, got %d", got)
	}
	def := DefaultConfig()
	if def.SessionRetentionDays != 7 || def.ActivityMaxRecords != 400000 {
		t.Errorf("DefaultConfig: session_retention_days=%d activity_max_records=%d", def.SessionRetentionDays, def.ActivityMaxRecords)
	}
}
