package config

import (
	"testing"
	"time"
)

func baseServer() *ServerConfig {
	iso := IsolationConfig{Image: "python:3.11"}
	return &ServerConfig{
		Name:        "srv",
		Command:     "uvx",
		Args:        []string{"some-package"},
		WorkingDir:  "/tmp/a",
		Env:         map[string]string{"A": "1"},
		Headers:     map[string]string{"X": "y"},
		Protocol:    "stdio",
		Enabled:     true,
		Quarantined: false,
		Isolation:   &iso,
		Created:     time.Now(),
	}
}

// TestConnectionEquivalent_DetectsEveryConnectionField is the recovery half of
// GH #1145: once a permanently failed server stops being re-dialed, the ONLY
// thing that brings it back automatically is the supervisor noticing that the
// user edited its connection settings. A field missing here is a user who fixes
// the problem and sees nothing happen.
func TestConnectionEquivalent_DetectsEveryConnectionField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerConfig)
	}{
		{"url", func(c *ServerConfig) { c.URL = "https://example.test/mcp" }},
		{"protocol", func(c *ServerConfig) { c.Protocol = "http" }},
		{"command", func(c *ServerConfig) { c.Command = "npx" }},
		{"args", func(c *ServerConfig) { c.Args = []string{"other-package"} }},
		{"args length", func(c *ServerConfig) { c.Args = []string{"some-package", "--flag"} }},
		{"working dir", func(c *ServerConfig) { c.WorkingDir = "/tmp/b" }},
		{"env value", func(c *ServerConfig) { c.Env = map[string]string{"A": "2"} }},
		{"env key added", func(c *ServerConfig) { c.Env = map[string]string{"A": "1", "B": "2"} }},
		{"headers", func(c *ServerConfig) { c.Headers = map[string]string{"X": "z"} }},
		{"enabled", func(c *ServerConfig) { c.Enabled = false }},
		{"quarantined", func(c *ServerConfig) { c.Quarantined = true }},
		{"isolation image", func(c *ServerConfig) { c.Isolation = &IsolationConfig{Image: "node:22"} }},
		{"isolation removed", func(c *ServerConfig) { c.Isolation = nil }},
		{"oauth added", func(c *ServerConfig) { c.OAuth = &OAuthConfig{ClientID: "abc"} }},
		{"init timeout", func(c *ServerConfig) { d := Duration(time.Minute); c.InitTimeout = &d }},
		{"launcher wait timeout", func(c *ServerConfig) { c.LauncherWaitTimeout = Duration(time.Minute) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := baseServer(), baseServer()
			if !ConnectionEquivalent(a, b) {
				t.Fatalf("two identical configs must compare equivalent")
			}
			tt.mutate(b)
			if ConnectionEquivalent(a, b) {
				t.Errorf("a change to %s must be detected as a connection change", tt.name)
			}
		})
	}
}

// TestConnectionEquivalent_IgnoresVolatileFields is the other half, and the
// trap: Updated is rewritten by unrelated config writes (quarantine decisions,
// tool-hash bookkeeping). A whole-struct fingerprint would therefore reconnect
// EVERY server on the next tick — a reconnect storm dressed up as a bug fix.
func TestConnectionEquivalent_IgnoresVolatileFields(t *testing.T) {
	a, b := baseServer(), baseServer()
	b.Created = a.Created.Add(-72 * time.Hour)
	b.Updated = time.Now()
	b.Name = a.Name // identity is keyed elsewhere; not a connection setting
	if !ConnectionEquivalent(a, b) {
		t.Errorf("created/updated churn must not count as a connection change")
	}
}

func TestConnectionEquivalent_NilHandling(t *testing.T) {
	if !ConnectionEquivalent(nil, nil) {
		t.Errorf("two absent configs are equivalent")
	}
	if ConnectionEquivalent(nil, baseServer()) || ConnectionEquivalent(baseServer(), nil) {
		t.Errorf("appearing/disappearing config is a change")
	}
}
