package config

import "testing"

func TestClientFeaturesEnabled(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name   string
		global *bool
		server *bool
		want   bool
	}{
		{"unset everywhere is on", nil, nil, true},
		{"server off", nil, &off, false},
		{"global off", &off, nil, false},
		{"global off wins over server on", &off, &on, false},
		{"both on", &on, &on, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{EnableClientFeatures: tt.global}
			if got := cfg.ClientFeaturesEnabled(&ServerConfig{ClientFeatures: tt.server}); got != tt.want {
				t.Fatalf("ClientFeaturesEnabled = %v, want %v", got, tt.want)
			}
		})
	}
	var nilCfg *Config
	if !nilCfg.ClientFeaturesEnabled(nil) {
		t.Fatal("a nil config means the default: on")
	}
}

// Capabilities are declared at initialize, so changing client_features must
// redial the server.
func TestConnectionEquivalent_ClientFeatures(t *testing.T) {
	off := false
	a := &ServerConfig{Name: "s", Command: "x"}
	b := &ServerConfig{Name: "s", Command: "x", ClientFeatures: &off}
	if ConnectionEquivalent(a, b) {
		t.Fatal("a client_features change must reconnect the server")
	}
	off2 := false
	if !ConnectionEquivalent(b, &ServerConfig{Name: "s", Command: "x", ClientFeatures: &off2}) {
		t.Fatal("equal values compare by value, not pointer")
	}
}
