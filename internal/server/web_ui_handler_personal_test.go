//go:build !server

package server

import "testing"

func TestWebUIHandler_PersonalBuildKeepsServerEditionCarrierOpaque(t *testing.T) {
	cfg := configFromJSON(t, `{"server_edition":{"enabled":true,"public_url":"https://operator-internal.example"}}`)
	assertServedEditionHint(t, cfg, false)
}
