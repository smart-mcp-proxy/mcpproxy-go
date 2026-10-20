package bundlesig

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates testdata/ from the deterministic helper below. Fixtures
// are checked in so a reviewer reads the exact bytes a conforming loader must
// accept or reject; TestFixturesAreStable fails if they drift from the helper.
var update = flag.Bool("update", false, "regenerate testdata fixtures")

// Constant seeds make the fixture keys reproducible. They are TEST keys: never
// configure either as a real trust anchor.
var (
	seedA = bytes.Repeat([]byte{0x01}, ed25519.SeedSize)
	seedB = bytes.Repeat([]byte{0x02}, ed25519.SeedSize)
)

func testKeyA() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(seedA) }
func testKeyB() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(seedB) }

func pubOf(priv ed25519.PrivateKey) ed25519.PublicKey {
	return priv.Public().(ed25519.PublicKey)
}

// zeroSig is the canonical 88-char encoding of 64 zero bytes.
var zeroSig = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))

// rawSidecar builds sidecar bytes from already-encoded JSON values, in the
// canonical key order, with no escaping, so tests can inject malformed values.
func rawSidecar(version, algorithm, keyID, signature string) []byte {
	return []byte(fmt.Sprintf(`{"sidecar_version":%s,"algorithm":%s,"key_id":%s,"signature":%s}`,
		version, algorithm, keyID, signature))
}

func quote(s string) string { return `"` + s + `"` }

// strSidecar quotes each field value (no escaping).
func strSidecar(version, algorithm, keyID, signature string) []byte {
	return rawSidecar(quote(version), quote(algorithm), quote(keyID), quote(signature))
}

// bundleFor returns a small manifest-shaped bundle claiming the given key id.
func bundleFor(keyID string) []byte {
	return []byte(fmt.Sprintf(`{"sequence":7,"key_id":%q,"trust_epoch":1,"generated_at":"2026-10-20T00:00:00Z","rules":[]}`+"\n", keyID))
}

// fixtureFiles is the full, deterministic content of testdata/. Sidecar
// fixtures are named <case>.sig; rows 1-10 are pure parse rejections, rows
// 11-13 are rejected by verification against valid.json (13: against its own
// bundle).
func fixtureFiles() map[string][]byte {
	a, b := testKeyA(), testKeyB()
	idA, idB := KeyID(pubOf(a)), KeyID(pubOf(b))
	bundle := bundleFor(idA)
	good := SignSidecar(a, bundle)
	// Every fixture ends in a newline: the repo's end-of-file pre-commit hook
	// would append one anyway, and fixtures must be byte-stable under it. The
	// no-newline form is exercised directly on SignSidecar output.
	nl := func(b []byte) []byte { return append(append([]byte{}, b...), '\n') }
	// pad returns spaces, then good, then a final newline, n bytes in total.
	// The padding leads rather than trails: the end-of-file hook rewrites a
	// file that ends in whitespace before its newline.
	pad := func(n int) []byte {
		return nl(append(bytes.Repeat([]byte(" "), n-len(good)-1), good...))
	}

	files := map[string][]byte{
		"valid.json":                    bundle,
		"valid.json.sig":                nl(good),
		"accept-trailing-newline.sig":   append(append([]byte{}, good...), '\r', '\n'),
		"accept-all-zero-signature.sig": strSidecar("1", "ed25519", idA, zeroSig),
		// Exactly MaxSidecarBytes (whitespace padded) is accepted, one more is not.
		"accept-4096-bytes.sig":        pad(MaxSidecarBytes),
		"reject-sidecar_too_large.sig": pad(MaxSidecarBytes + 1),
		// Row 2: trailing non-whitespace bytes after a valid object.
		"reject-sidecar_malformed.sig": append(append([]byte{}, good...), 'x'),
		// Row 3: signature absent.
		"reject-sidecar_missing_field.sig": []byte(
			fmt.Sprintf(`{"sidecar_version":"1","algorithm":"ed25519","key_id":%q}`, idA)),
		// Row 4: all four present plus a fifth (the dropped `sequence` duplicate).
		"reject-sidecar_unknown_field.sig": []byte(
			fmt.Sprintf(`{"sidecar_version":"1","algorithm":"ed25519","key_id":%q,"signature":%q,"sequence":"7"}`, idA, zeroSig)),
		// Row 5: encoding/json would silently take the last "algorithm".
		"reject-sidecar_duplicate_field.sig": []byte(
			fmt.Sprintf(`{"sidecar_version":"1","algorithm":"ed25519","algorithm":"rsa","key_id":%q,"signature":%q}`, idA, zeroSig)),
		// Row 6: sidecar_version is the number 1, not the string "1".
		"reject-sidecar_type.sig":                  rawSidecar("1", quote("ed25519"), quote(idA), quote(zeroSig)),
		"reject-sidecar_version_unsupported.sig":   strSidecar("2", "ed25519", idA, zeroSig),
		"reject-sidecar_algorithm_unsupported.sig": strSidecar("1", "rsa", idA, zeroSig),
		"reject-sidecar_key_id_malformed.sig":      strSidecar("1", "ed25519", "ABCDEF", zeroSig),
		"reject-sidecar_signature_malformed.sig":   strSidecar("1", "ed25519", idA, zeroSig[:87]),
		// Row 11: well-formed, signed by a key outside the trust set {A}.
		"reject-key_not_trusted.sig": SignSidecar(b, bundle),
		// Row 12: trusted key id, signature of 64 zero bytes.
		"reject-signature_invalid.sig": strSidecar("1", "ed25519", idA, zeroSig),
		// Row 13: genuine signature by A over a bundle whose manifest names B.
		"reject-key_id_mismatch.bundle.json": bundleFor(idB),
		"reject-key_id_mismatch.sig":         SignSidecar(a, bundleFor(idB)),
	}
	for name, data := range files {
		if !bytes.HasSuffix(data, []byte("\n")) {
			files[name] = nl(data)
		}
	}
	return files
}

func TestFixturesAreStable(t *testing.T) {
	want := fixtureFiles()
	if *update {
		for name, data := range want {
			if err := os.WriteFile(filepath.Join("testdata", name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, data := range want {
		got, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("fixture %s: %v (run go test -update)", name, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("fixture %s drifted from the deterministic helper (run go test -update and review the diff)", name)
		}
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := want[e.Name()]; !ok {
			t.Errorf("testdata/%s is not produced by the helper", e.Name())
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
