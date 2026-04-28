package main

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

func TestConnectClientRegistry_IncludesOpenCode(t *testing.T) {
	clients := connect.GetAllClients()
	found := false
	for _, c := range clients {
		if c.ID == "opencode" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected opencode in client registry")
	}
}
