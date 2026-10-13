//go:build server

package server

import "testing"

func TestWebUIHandler_ServerBuildUsesConfiguredEditionState(t *testing.T) {
	tests := []struct {
		name string
		cfg  string
		want bool
	}{
		{name: "nil config", want: false},
		{name: "absent block", cfg: `{}`, want: false},
		{name: "disabled block", cfg: `{"server_edition":{"enabled":false,"public_url":"https://operator-internal.example"}}`, want: false},
		{name: "enabled block", cfg: `{"server_edition":{"enabled":true,"public_url":"https://operator-internal.example"}}`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg == "" {
				assertServedEditionHint(t, nil, tt.want)
				return
			}
			assertServedEditionHint(t, configFromJSON(t, tt.cfg), tt.want)
		})
	}
}
