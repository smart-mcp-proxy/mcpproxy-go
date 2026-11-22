package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func noLinks(t *testing.T) {
	t.Helper()
	orig := linkFile
	linkFile = func(string, string) error { return errors.New("hard links are not supported") }
	t.Cleanup(func() { linkFile = orig })
}

// Without hard links the fallback publishes the COMPLETE temp file by rename: the
// key file is never visible empty, and the bytes on disk are what is returned.
func TestGetOrCreateHMACKey_NoHardLinkFallbackPublishesACompleteFile(t *testing.T) {
	noLinks(t)
	dir := t.TempDir()
	got, err := GetOrCreateHMACKey(dir)
	if err != nil || len(got) != 32 {
		t.Fatalf("got %d bytes err %v", len(got), err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, hmacKeyFile))
	if !bytes.Equal(onDisk, got) {
		t.Fatal("the returned key is not the one on disk")
	}
	info, _ := os.Stat(filepath.Join(dir, hmacKeyFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %v", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	// A second call reads the same key.
	again, _ := GetOrCreateHMACKey(dir)
	if !bytes.Equal(again, got) {
		t.Fatal("second call returned another key")
	}
}

// An empty or wrong-size target is recovered on every filesystem.
func TestGetOrCreateHMACKey_EmptyTargetIsRecovered(t *testing.T) {
	for name, withLinks := range map[string]bool{"links": true, "no links": false} {
		t.Run(name, func(t *testing.T) {
			if !withLinks {
				noLinks(t)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, hmacKeyFile), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := GetOrCreateHMACKey(dir)
			if err != nil || len(got) != 32 {
				t.Fatalf("got %d bytes err %v", len(got), err)
			}
			onDisk, _ := os.ReadFile(filepath.Join(dir, hmacKeyFile))
			if !bytes.Equal(onDisk, got) {
				t.Fatal("not repaired on disk")
			}
		})
	}
}
