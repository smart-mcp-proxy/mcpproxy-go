//go:build darwin

package main

import "testing"

func TestShellQuote(t *testing.T) {
	tcases := map[string]string{
		"":         "''",
		"simple":   "'simple'",
		"with spa": "'with spa'",
		"a'b":      "'a'\\''b'",
	}

	for input, expected := range tcases {
		if got := shellQuote(input); got != expected {
			t.Fatalf("shellQuote(%q) = %q, expected %q", input, got, expected)
		}
	}
}

func TestBuildShellExecCommand(t *testing.T) {
	cmd := buildShellExecCommand("/usr/local/bin/mcpproxy", []string{"serve", "--listen", "127.0.0.1:8080"})
	expected := "exec '/usr/local/bin/mcpproxy' 'serve' '--listen' '127.0.0.1:8080'"
	if cmd != expected {
		t.Fatalf("buildShellExecCommand produced %q, expected %q", cmd, expected)
	}
}
