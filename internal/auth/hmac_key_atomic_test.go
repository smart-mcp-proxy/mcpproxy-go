package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Two first-time callers must never end up with different keys: every caller of
// GetOrCreateHMACKey on an empty directory gets the one key the file holds.
func TestGetOrCreateHMACKey_ConcurrentFirstCallersAgree(t *testing.T) {
	dir := t.TempDir()
	const n = 32
	keys := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i], errs[i] = GetOrCreateHMACKey(dir)
		}(i)
	}
	close(start)
	wg.Wait()

	onDisk, err := os.ReadFile(filepath.Join(dir, hmacKeyFile))
	if err != nil || len(onDisk) != 32 {
		t.Fatalf("key file: %v len=%d", err, len(onDisk))
	}
	for i := range keys {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if !bytes.Equal(keys[i], onDisk) {
			t.Fatalf("caller %d got a key that is not the one on disk", i)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, hmacKeyFile))
	// Windows reports 0666 for any writable file; the mode bits mean nothing there.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files were left behind: %v", entries)
	}
}

func TestGetOrCreateHMACKey_ExistingKeyIsReturnedUnchanged(t *testing.T) {
	dir := t.TempDir()
	want := bytes.Repeat([]byte{7}, 32)
	if err := os.WriteFile(filepath.Join(dir, hmacKeyFile), want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GetOrCreateHMACKey(dir)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %x err %v", got, err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, hmacKeyFile))
	if !bytes.Equal(onDisk, want) {
		t.Fatal("an existing key file was rewritten")
	}
}

// A file of the wrong size is replaced, as it always was.
func TestGetOrCreateHMACKey_WrongSizeFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, hmacKeyFile), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GetOrCreateHMACKey(dir)
	if err != nil || len(got) != 32 {
		t.Fatalf("got %d bytes, err %v", len(got), err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, hmacKeyFile))
	if !bytes.Equal(onDisk, got) {
		t.Fatal("the replacement is not on disk")
	}
}
