package main

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// keygen creates a key pair in a fresh dir and returns the dir, the base64
// public key and its key id as printed.
func keygen(t *testing.T) (dir, pub, keyID string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "keys")
	code, out, errs := runCLI(t, "keygen", "--out", dir)
	if code != 0 {
		t.Fatalf("keygen exit %d: %s", code, errs)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "public_key: "):
			pub = strings.TrimPrefix(line, "public_key: ")
		case strings.HasPrefix(line, "key_id: "):
			keyID = strings.TrimPrefix(line, "key_id: ")
		}
	}
	if pub == "" || keyID == "" {
		t.Fatalf("keygen output lacks public_key/key_id:\n%s", out)
	}
	return dir, pub, keyID
}

func writeBundle(t *testing.T, keyID string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "scanner-bundle.json")
	body := fmt.Sprintf(`{"sequence":3,"key_id":%q,"trust_epoch":1,"generated_at":"2026-10-20T00:00:00Z","rules":[]}`+"\n", keyID)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKeygenFilesAndRefusesOverwrite(t *testing.T) {
	dir, pubB64, keyID := keygen(t)

	st, err := os.Stat(filepath.Join(dir, "operator.key"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("operator.key mode = %v, want 0600", st.Mode().Perm())
	}
	pubFile, err := os.ReadFile(filepath.Join(dir, "operator.pub"))
	if err != nil || strings.TrimSpace(string(pubFile)) != pubB64 {
		t.Fatalf("operator.pub = %q, %v", pubFile, err)
	}
	pub, err := bundlesig.ParsePublicKey(pubB64)
	if err != nil || bundlesig.KeyID(pub) != keyID {
		t.Fatalf("printed key id does not match printed public key: %v", err)
	}

	before, _ := os.ReadFile(filepath.Join(dir, "operator.key"))
	code, _, errs := runCLI(t, "keygen", "--out", dir)
	if code == 0 || !strings.Contains(errs, "exists") {
		t.Fatalf("second keygen: exit %d, stderr %q; want refusal", code, errs)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "operator.key"))
	if !bytes.Equal(before, after) {
		t.Fatal("keygen overwrote an existing key")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	dir, pub, keyID := keygen(t)
	bundle := writeBundle(t, keyID)
	orig, _ := os.ReadFile(bundle)

	code, _, errs := runCLI(t, "sign", "--bundle", bundle, "--key", filepath.Join(dir, "operator.key"))
	if code != 0 {
		t.Fatalf("sign exit %d: %s", code, errs)
	}
	// In place and byte-preserving: sign never edits bundle bytes.
	if now, _ := os.ReadFile(bundle); !bytes.Equal(now, orig) {
		t.Fatal("sign modified the bundle bytes")
	}
	sidecar, err := os.ReadFile(bundle + ".sig")
	if err != nil {
		t.Fatalf("no sidecar: %v", err)
	}
	if _, err := bundlesig.ParseSidecar(sidecar); err != nil {
		t.Fatalf("sidecar does not parse: %v", err)
	}

	code, out, errs := runCLI(t, "verify", "--bundle", bundle, "--signer", pub)
	if code != 0 || strings.TrimSpace(out) != "OK" {
		t.Fatalf("verify exit %d out %q err %q", code, out, errs)
	}

	// A tampered bundle is rejected with the contract code and exit 1.
	if err := os.WriteFile(bundle, append(orig, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runCLI(t, "verify", "--bundle", bundle, "--signer", pub)
	if code != 1 || !strings.Contains(out+errs, bundlesig.CodeSignatureInvalid) {
		t.Fatalf("tampered verify: exit %d out %q err %q", code, out, errs)
	}
}

func TestSignOutCopy(t *testing.T) {
	dir, pub, keyID := keygen(t)
	bundle := writeBundle(t, keyID)
	out := filepath.Join(t.TempDir(), "published", "bundle.json")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runCLI(t, "sign", "--bundle", bundle, "--key", filepath.Join(dir, "operator.key"), "--out", out); code != 0 {
		t.Fatalf("sign exit %d: %s", code, errs)
	}
	a, _ := os.ReadFile(bundle)
	b, _ := os.ReadFile(out)
	if !bytes.Equal(a, b) {
		t.Fatal("--out copy differs from the source bundle")
	}
	if _, err := os.Stat(bundle + ".sig"); err == nil {
		t.Fatal("sidecar written next to the source instead of --out")
	}
	if code, o, e := runCLI(t, "verify", "--bundle", out, "--signer", pub); code != 0 {
		t.Fatalf("verify out copy: exit %d %q %q", code, o, e)
	}
}

// A bundle whose manifest key_id is not the signing key's id would produce an
// artifact every loader rejects at row 13, so sign refuses to produce it.
func TestSignRefusesKeyIDMismatch(t *testing.T) {
	dir, _, _ := keygen(t)
	_, _, otherID := keygen(t)
	bundle := writeBundle(t, otherID)

	code, _, errs := runCLI(t, "sign", "--bundle", bundle, "--key", filepath.Join(dir, "operator.key"))
	if code == 0 || !strings.Contains(errs, bundlesig.CodeKeyIDMismatch) {
		t.Fatalf("sign: exit %d stderr %q; want key_id_mismatch refusal", code, errs)
	}
	if _, err := os.Stat(bundle + ".sig"); err == nil {
		t.Fatal("a sidecar was written despite the refusal")
	}
}

func TestSignRefusesBundleWithoutKeyID(t *testing.T) {
	dir, _, _ := keygen(t)
	p := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(p, []byte(`{"sequence":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, "sign", "--bundle", p, "--key", filepath.Join(dir, "operator.key")); code == 0 {
		t.Fatal("signed a bundle with no manifest key_id")
	}
	if err := os.WriteFile(p, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, "sign", "--bundle", p, "--key", filepath.Join(dir, "operator.key")); code == 0 {
		t.Fatal("signed a non-JSON bundle")
	}
}

func TestVerifyWrongSignerAndMissingSidecar(t *testing.T) {
	dir, _, keyID := keygen(t)
	_, otherPub, _ := keygen(t)
	bundle := writeBundle(t, keyID)

	if code, _, _ := runCLI(t, "verify", "--bundle", bundle, "--signer", otherPub); code != 1 {
		t.Fatalf("verify with no sidecar: exit %d, want 1", code)
	}
	if code, _, errs := runCLI(t, "sign", "--bundle", bundle, "--key", filepath.Join(dir, "operator.key")); code != 0 {
		t.Fatal(errs)
	}
	code, out, errs := runCLI(t, "verify", "--bundle", bundle, "--signer", otherPub)
	if code != 1 || !strings.Contains(out+errs, bundlesig.CodeKeyNotTrusted) {
		t.Fatalf("wrong signer: exit %d out %q err %q", code, out, errs)
	}
}

// verify enforces row 13 against the manifest: a genuine signature by the
// trusted key over a bundle whose manifest names someone else.
func TestVerifyManifestKeyIDMismatch(t *testing.T) {
	_, _, otherID := keygen(t)
	// Sign with a throwaway key by hand so the CLI's own refusal is bypassed.
	dir2 := t.TempDir()
	seed := bytes.Repeat([]byte{9}, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	bundle := filepath.Join(dir2, "b.json")
	body := []byte(fmt.Sprintf(`{"sequence":1,"key_id":%q}`, otherID))
	if err := os.WriteFile(bundle, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle+".sig", bundlesig.SignSidecar(priv, body), 0o644); err != nil {
		t.Fatal(err)
	}
	signer := base64Std(priv.Public().(ed25519.PublicKey))
	code, out, errs := runCLI(t, "verify", "--bundle", bundle, "--signer", signer)
	if code != 1 || !strings.Contains(out+errs, bundlesig.CodeKeyIDMismatch) {
		t.Fatalf("exit %d out %q err %q; want key_id_mismatch", code, out, errs)
	}
}

func TestUsage(t *testing.T) {
	if code, _, _ := runCLI(t); code != 2 {
		t.Fatalf("no args: exit %d, want 2", code)
	}
	if code, _, _ := runCLI(t, "frobnicate"); code != 2 {
		t.Fatalf("unknown subcommand: exit %d, want 2", code)
	}
	if code, _, _ := runCLI(t, "sign"); code != 2 {
		t.Fatalf("sign without flags: exit %d, want 2", code)
	}
}

// TestSignDoesNotWriteThroughASidecarSymlink: someone who can write the bundle
// directory plants <bundle>.sig as a symlink to a file they cannot write
// themselves (the operator key, mcp_config.json). The operator's next sign must
// replace the directory entry, never truncate the symlink's target.
func TestSignDoesNotWriteThroughASidecarSymlink(t *testing.T) {
	dir, _, keyID := keygen(t)
	bundle := writeBundle(t, keyID)
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, bundle+".sig"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code, _, errs := runCLI(t, "sign", "--bundle", bundle, "--key", filepath.Join(dir, "operator.key")); code != 0 {
		t.Fatalf("sign exit %d: %s", code, errs)
	}
	if got, _ := os.ReadFile(victim); string(got) != "precious" {
		t.Fatalf("sign wrote through the planted symlink: victim now %q", got)
	}
	st, err := os.Lstat(bundle + ".sig")
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("sidecar should now be a regular file: %v %v", st, err)
	}
}

// TestVerifyBoundsItsReads: verify reads the sidecar first through the 4 KiB
// cap and refuses non-regular files instead of reading them unbounded.
func TestVerifyBoundsItsReads(t *testing.T) {
	_, pub, keyID := keygen(t)
	bundle := writeBundle(t, keyID)
	if err := os.WriteFile(bundle+".sig", bytes.Repeat([]byte(" "), bundlesig.MaxSidecarBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runCLI(t, "verify", "--bundle", bundle, "--signer", pub)
	if code != 1 || !strings.Contains(errs, "exceeds") {
		t.Fatalf("oversized sidecar: exit %d, stderr %q", code, errs)
	}
	// The machine-readable contract code is still printed (row 1).
	_, out, _ := runCLI(t, "verify", "--bundle", bundle, "--signer", pub)
	if strings.TrimSpace(out) != bundlesig.CodeSidecarTooLarge {
		t.Fatalf("oversized sidecar: stdout %q, want %q", out, bundlesig.CodeSidecarTooLarge)
	}

	if err := os.Remove(bundle + ".sig"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bundle, bundle+".sig"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	code, _, errs = runCLI(t, "verify", "--bundle", bundle, "--signer", pub)
	if code != 1 || !strings.Contains(errs, "not a regular file") {
		t.Fatalf("symlinked sidecar: exit %d, stderr %q", code, errs)
	}
}
