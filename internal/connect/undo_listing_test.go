package connect

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The backup an Undo restores is addressed by the name the directory lists,
// and only a file that is really there is found.
func TestListedFileName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.json.bak.1"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, found, err := listedFileName(dir, "c.json.bak.1")
	if err != nil || !found || name != "c.json.bak.1" {
		t.Fatalf("listed file: name=%q found=%v err=%v", name, found, err)
	}
	for _, missing := range []string{"c.json.bak.2", "..", ".", ""} {
		if _, found, err := listedFileName(dir, missing); err != nil || found {
			t.Fatalf("%q: found=%v err=%v, want not found", missing, found, err)
		}
	}
	if _, found, err := listedFileName(filepath.Join(dir, "absent"), "c.json.bak.1"); err != nil || found {
		t.Fatalf("missing directory: found=%v err=%v, want not found", found, err)
	}
}

// Records written before this change must still match: the digest is the
// plain SHA-256 of the file.
func TestFileDigestIsPlainSHA256(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("abc"), []byte("{\"mcpServers\":{}}\n")} {
		sum := sha256.Sum256(in)
		if got, want := fileDigest(in), hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("fileDigest(%q) = %s, want %s", in, got, want)
		}
	}
}
