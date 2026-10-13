//go:build !server

package config

import (
	"encoding/json"
	"testing"
)

func TestServerEditionEnabled_PersonalBuildKeepsCarrierOpaque(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"server_edition":{"enabled":true,"provider_url":"https://secret.example"}}`), &cfg); err != nil {
		t.Fatalf("unmarshal opaque server edition carrier: %v", err)
	}
	if ServerEditionEnabled(&cfg) {
		t.Fatal("personal build must not interpret the opaque server_edition carrier")
	}
}
