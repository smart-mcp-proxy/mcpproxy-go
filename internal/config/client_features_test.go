package config

import (
	"slices"
	"testing"
)

func TestClientFeaturesFor(t *testing.T) {
	on, off := true, false
	all := []string{"roots", "sampling", "elicitation"}
	tests := []struct {
		name   string
		global *bool
		server *ServerConfig
		want   []string
	}{
		{"no server", nil, nil, nil},
		{"unset is none", nil, &ServerConfig{}, nil},
		{"empty is none", &on, &ServerConfig{ClientFeatures: []string{}}, nil},
		{"only what is listed", nil, &ServerConfig{ClientFeatures: []string{"elicitation"}}, []string{"elicitation"}},
		{"all", &on, &ServerConfig{ClientFeatures: all}, []string{"elicitation", "sampling", "roots"}},
		{"unknown grants nothing", nil, &ServerConfig{ClientFeatures: []string{"everything"}}, nil},
		{"kill switch", &off, &ServerConfig{ClientFeatures: all}, nil},
		{"quarantined", nil, &ServerConfig{ClientFeatures: all, Quarantined: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{EnableClientFeatures: tt.global}
			if got := cfg.ClientFeaturesFor(tt.server); !slices.Equal(got, tt.want) {
				t.Fatalf("ClientFeaturesFor = %v, want %v", got, tt.want)
			}
		})
	}
	var nilCfg *Config
	if got := nilCfg.ClientFeaturesFor(&ServerConfig{ClientFeatures: []string{"roots"}}); !slices.Equal(got, []string{"roots"}) {
		t.Fatalf("a nil config means the default: the server's list, got %v", got)
	}
}

func TestClientFeaturesValidation(t *testing.T) {
	if errs := ClientFeaturesValidationErrors("client_features", ClientFeatureNames); len(errs) != 0 {
		t.Fatalf("known names refused: %v", errs)
	}
	if errs := ClientFeaturesValidationErrors("client_features", []string{"roots", "Sampling"}); len(errs) != 1 {
		t.Fatalf("want one error for the unknown name, got %v", errs)
	}
}

func TestMergeServerConfig_ClientFeatures(t *testing.T) {
	base := &ServerConfig{Name: "s", Command: "x", ClientFeatures: []string{"roots"}}
	for _, tt := range []struct {
		name  string
		patch []string
		want  []string
	}{
		{"omitted keeps", nil, []string{"roots"}},
		{"replaces", []string{"elicitation"}, []string{"elicitation"}},
		{"empty clears", []string{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			merged, _, err := MergeServerConfig(base, &ServerConfig{ClientFeatures: tt.patch}, DefaultMergeOptions())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(merged.ClientFeatures, tt.want) {
				t.Fatalf("client_features = %v, want %v", merged.ClientFeatures, tt.want)
			}
		})
	}
}

// Capabilities are declared at initialize, so changing client_features must
// redial the server.
func TestConnectionEquivalent_ClientFeatures(t *testing.T) {
	a := &ServerConfig{Name: "s", Command: "x"}
	b := &ServerConfig{Name: "s", Command: "x", ClientFeatures: []string{"roots"}}
	if ConnectionEquivalent(a, b) {
		t.Fatal("a client_features change must reconnect the server")
	}
	if !ConnectionEquivalent(a, &ServerConfig{Name: "s", Command: "x", ClientFeatures: []string{}}) {
		t.Fatal("unset and empty are the same: none")
	}
}
