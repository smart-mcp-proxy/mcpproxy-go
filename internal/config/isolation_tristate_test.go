package config

import "testing"

// TestIsolationConfig_TriStatePredicates pins the tri-state semantics of
// IsolationConfig.Enabled (GH #1142). The field is a *bool where nil means
// "inherit the global isolation setting" — NOT "disabled". The old
// IsEnabled() helper collapsed nil to false, which is why an inheriting
// server was reported as unisolated across the REST/MCP surfaces.
//
// The replacement predicates are named for the question they answer so the
// nil→false trap cannot be reintroduced by a careless call site.
func TestIsolationConfig_TriStatePredicates(t *testing.T) {
	tests := []struct {
		name              string
		cfg               *IsolationConfig
		wantHasOverride   bool
		wantExplicitTrue  bool
		wantExplicitFalse bool
	}{
		{
			name:              "nil config",
			cfg:               nil,
			wantHasOverride:   false,
			wantExplicitTrue:  false,
			wantExplicitFalse: false,
		},
		{
			name:              "nil Enabled means inherit global",
			cfg:               &IsolationConfig{Image: "python:3.12"},
			wantHasOverride:   false,
			wantExplicitTrue:  false,
			wantExplicitFalse: false,
		},
		{
			name:              "explicit true",
			cfg:               &IsolationConfig{Enabled: BoolPtr(true)},
			wantHasOverride:   true,
			wantExplicitTrue:  true,
			wantExplicitFalse: false,
		},
		{
			name:              "explicit false",
			cfg:               &IsolationConfig{Enabled: BoolPtr(false)},
			wantHasOverride:   true,
			wantExplicitTrue:  false,
			wantExplicitFalse: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.HasEnabledOverride(); got != tt.wantHasOverride {
				t.Errorf("HasEnabledOverride() = %v, want %v", got, tt.wantHasOverride)
			}
			if got := tt.cfg.IsExplicitlyEnabled(); got != tt.wantExplicitTrue {
				t.Errorf("IsExplicitlyEnabled() = %v, want %v", got, tt.wantExplicitTrue)
			}
			if got := tt.cfg.IsExplicitlyDisabled(); got != tt.wantExplicitFalse {
				t.Errorf("IsExplicitlyDisabled() = %v, want %v", got, tt.wantExplicitFalse)
			}
		})
	}
}
