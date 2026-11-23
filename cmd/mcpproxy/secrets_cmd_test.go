package main

import (
	"strings"
	"testing"
)

func TestReadSecretStdinPreservesValueAndRemovesLineEndings(t *testing.T) {
	got, err := readSecretStdin(strings.NewReader(" token with spaces \r\n"))
	if err != nil {
		t.Fatalf("readSecretStdin() error = %v", err)
	}
	if want := " token with spaces "; got != want {
		t.Fatalf("readSecretStdin() = %q, want %q", got, want)
	}
}

func TestReadSecretStdinAllowsEmptyInputForCallerValidation(t *testing.T) {
	got, err := readSecretStdin(strings.NewReader(""))
	if err != nil {
		t.Fatalf("readSecretStdin() error = %v", err)
	}
	if got != "" {
		t.Fatalf("readSecretStdin() = %q, want empty input", got)
	}
}
