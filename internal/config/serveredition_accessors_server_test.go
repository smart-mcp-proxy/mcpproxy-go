//go:build server

package config

import "testing"

func TestServerEditionEnabled_ReportsEnabledServerConfiguration(t *testing.T) {
	cfg := &Config{ServerEdition: &ServerEditionConfig{Enabled: true}}
	if !ServerEditionEnabled(cfg) {
		t.Fatal("server build must report an enabled server_edition block")
	}
}
