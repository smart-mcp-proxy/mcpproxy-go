package bundlesig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func trustA(t *testing.T) *TrustSet {
	t.Helper()
	a := testKeyA()
	ts, err := NewTrustSet(Key{ID: KeyID(pubOf(a)), Public: pubOf(a), Authority: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestKeyID(t *testing.T) {
	pub := pubOf(testKeyA())
	sum := sha256.Sum256(pub)
	if got, want := KeyID(pub), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("KeyID = %q want %q", got, want)
	}
	if id := KeyID(pub); len(id) != 64 || id != strings.ToLower(id) {
		t.Fatal("key id must be 64 lowercase hex")
	}
}

func TestParsePublicKey(t *testing.T) {
	pub := pubOf(testKeyA())
	enc := base64.StdEncoding.EncodeToString(pub)
	got, err := ParsePublicKey(enc)
	if err != nil || !bytes.Equal(got, pub) {
		t.Fatalf("round trip: %v", err)
	}
	bad := map[string]string{
		"empty":         "",
		"31 bytes":      base64.StdEncoding.EncodeToString(pub[:31]),
		"33 bytes":      base64.StdEncoding.EncodeToString(append(append([]byte{}, pub...), 0)),
		"url alphabet":  base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32)),
		"raw (no pad)":  base64.RawStdEncoding.EncodeToString(pub),
		"newline":       enc[:10] + "\n" + enc[10:],
		"trailing junk": enc + "!",
		// 32 bytes -> 43 data chars + "="; "B=" last char has nonzero padding bits.
		"non-canonical": base64.StdEncoding.EncodeToString(make([]byte, 32))[:42] + "B=",
	}
	for name, s := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePublicKey(s); err == nil {
				t.Fatalf("ParsePublicKey(%q) accepted", s)
			}
		})
	}
}

func TestNewTrustSet(t *testing.T) {
	a, b := testKeyA(), testKeyB()
	ka := Key{ID: KeyID(pubOf(a)), Public: pubOf(a), Authority: "operator"}
	kb := Key{ID: KeyID(pubOf(b)), Public: pubOf(b), Authority: "baseline"}

	ts, err := NewTrustSet(ka, kb)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []Key{ka, kb} {
		got, ok := ts.Lookup(k.ID)
		if !ok || got.Authority != k.Authority || !bytes.Equal(got.Public, k.Public) {
			t.Fatalf("Lookup(%s) = %+v, %v", k.ID, got, ok)
		}
	}
	if _, ok := ts.Lookup(KeyID(ed25519.PublicKey(make([]byte, 32)))); ok {
		t.Fatal("unknown id found")
	}

	// A duplicate id is refused even across authorities: otherwise one key
	// could satisfy whichever root happens to be active.
	if _, err := NewTrustSet(ka, Key{ID: ka.ID, Public: ka.Public, Authority: "baseline"}); err == nil {
		t.Fatal("cross-authority duplicate id accepted")
	}
	if _, err := NewTrustSet(ka, ka); err == nil {
		t.Fatal("same-authority duplicate id accepted")
	}
	if _, err := NewTrustSet(Key{ID: KeyID(pubOf(b)), Public: pubOf(a), Authority: "operator"}); err == nil {
		t.Fatal("id that is not the fingerprint of Public accepted")
	}
	if _, err := NewTrustSet(Key{ID: "x", Public: ed25519.PublicKey(make([]byte, 31))}); err == nil {
		t.Fatal("wrong-length key accepted")
	}
	if _, err := NewTrustSet(Key{ID: KeyID(nil), Public: nil}); err == nil {
		t.Fatal("nil key accepted")
	}

	// The set owns its key bytes: mutating the caller's slice afterwards must
	// not change what verifies.
	mutable := append(ed25519.PublicKey{}, pubOf(a)...)
	ts2, err := NewTrustSet(Key{ID: ka.ID, Public: mutable, Authority: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	mutable[0] ^= 0xff
	got, _ := ts2.Lookup(ka.ID)
	if !bytes.Equal(got.Public, pubOf(a)) {
		t.Fatal("trust set aliases caller's key bytes")
	}
}

func TestVerifyValidFixture(t *testing.T) {
	sc, err := ParseSidecar(readFixture(t, "valid.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := trustA(t).Verify(readFixture(t, "valid.json"), sc)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if k.ID != sc.KeyID || k.Authority != "operator" {
		t.Fatalf("returned key = %+v", k)
	}
}

func TestVerifyRejectFixtures(t *testing.T) {
	valid := readFixture(t, "valid.json")
	for _, tc := range []struct{ fixture, code string }{
		{"reject-key_not_trusted.sig", CodeKeyNotTrusted},
		{"reject-signature_invalid.sig", CodeSignatureInvalid},
		{"accept-all-zero-signature.sig", CodeSignatureInvalid}, // parses, never verifies
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			sc, err := ParseSidecar(readFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("fixture must parse: %v", err)
			}
			_, err = trustA(t).Verify(valid, sc)
			wantCode(t, err, tc.code)
		})
	}

	// Row 13: the signature is genuine, but the manifest names another key.
	bundle := readFixture(t, "reject-key_id_mismatch.bundle.json")
	sc, err := ParseSidecar(readFixture(t, "reject-key_id_mismatch.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := trustA(t).Verify(bundle, sc)
	if err != nil {
		t.Fatalf("signature must verify: %v", err)
	}
	wantCode(t, CheckKeyIDAgreement(KeyID(pubOf(testKeyB())), sc, verified), CodeKeyIDMismatch)
}

func TestVerifyRowOrder(t *testing.T) {
	// Row 11 precedes row 12: an untrusted key with a garbage signature is
	// key_not_trusted, not signature_invalid.
	other := KeyID(pubOf(testKeyB()))
	sc, err := ParseSidecar(strSidecar("1", "ed25519", other, zeroSig))
	if err != nil {
		t.Fatal(err)
	}
	_, err = trustA(t).Verify(readFixture(t, "valid.json"), sc)
	wantCode(t, err, CodeKeyNotTrusted)
}

// TestVerifyDoesNotDecideActiveAuthority: Verify searches the WHOLE set, so a
// genuine key of the non-active root verifies here; the caller then rules on
// authority (-> authority_not_active upstream, not key_not_trusted).
func TestVerifyDoesNotDecideActiveAuthority(t *testing.T) {
	a, b := testKeyA(), testKeyB()
	ts, err := NewTrustSet(
		Key{ID: KeyID(pubOf(a)), Public: pubOf(a), Authority: "operator"},
		Key{ID: KeyID(pubOf(b)), Public: pubOf(b), Authority: "baseline"},
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundleFor(KeyID(pubOf(b)))
	sc, err := ParseSidecar(SignSidecar(b, bundle))
	if err != nil {
		t.Fatal(err)
	}
	k, err := ts.Verify(bundle, sc)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if k.Authority != "baseline" {
		t.Fatalf("authority = %q", k.Authority)
	}
}

// TestTamperClasses covers the four SC-001 tamper classes at this layer.
func TestTamperClasses(t *testing.T) {
	a := testKeyA()
	bundle := readFixture(t, "valid.json")
	good := readFixture(t, "valid.json.sig")
	ts := trustA(t)

	verify := func(bundle, sidecar []byte) error {
		sc, err := ParseSidecar(sidecar)
		if err != nil {
			return err
		}
		_, err = ts.Verify(bundle, sc)
		return err
	}
	if err := verify(bundle, good); err != nil {
		t.Fatalf("control: %v", err)
	}

	t.Run("flipped bundle byte", func(t *testing.T) {
		for i := range bundle {
			tampered := append([]byte{}, bundle...)
			tampered[i] ^= 0x01
			wantCode(t, verify(tampered, good), CodeSignatureInvalid)
		}
	})
	t.Run("appended and truncated bundle", func(t *testing.T) {
		wantCode(t, verify(append(append([]byte{}, bundle...), ' '), good), CodeSignatureInvalid)
		wantCode(t, verify(bundle[:len(bundle)-1], good), CodeSignatureInvalid)
	})
	t.Run("altered signature value", func(t *testing.T) {
		sc, _ := ParseSidecar(good)
		sc.Signature[10] ^= 0x01
		altered := rawSidecar(`"1"`, `"ed25519"`, quote(sc.KeyID), quote(base64.StdEncoding.EncodeToString(sc.Signature)))
		wantCode(t, verify(bundle, altered), CodeSignatureInvalid)
	})
	t.Run("altered key_id (unknown id)", func(t *testing.T) {
		sc, _ := ParseSidecar(good)
		altered := strSidecar("1", "ed25519", KeyID(pubOf(testKeyB())), base64.StdEncoding.EncodeToString(sc.Signature))
		wantCode(t, verify(bundle, altered), CodeKeyNotTrusted)
	})
	t.Run("altered key_id (another trusted key)", func(t *testing.T) {
		// Both keys trusted; the sidecar claims B but carries A's signature:
		// verification under B fails, it never falls back to A.
		b := testKeyB()
		both, err := NewTrustSet(
			Key{ID: KeyID(pubOf(a)), Public: pubOf(a), Authority: "operator"},
			Key{ID: KeyID(pubOf(b)), Public: pubOf(b), Authority: "operator"},
		)
		if err != nil {
			t.Fatal(err)
		}
		sc, _ := ParseSidecar(good)
		sc.KeyID = KeyID(pubOf(b))
		_, err = both.Verify(bundle, sc)
		wantCode(t, err, CodeSignatureInvalid)
	})
	t.Run("altered manifest key_id", func(t *testing.T) {
		sc, _ := ParseSidecar(good)
		verified, err := ts.Verify(bundle, sc)
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, CheckKeyIDAgreement(KeyID(pubOf(testKeyB())), sc, verified), CodeKeyIDMismatch)
	})
	t.Run("sidecar of bundle A against bundle B", func(t *testing.T) {
		other := bundleFor(KeyID(pubOf(a)))
		other = bytes.Replace(other, []byte(`"sequence":7`), []byte(`"sequence":8`), 1)
		wantCode(t, verify(other, good), CodeSignatureInvalid)
	})
}

func TestCheckKeyIDAgreement(t *testing.T) {
	a := testKeyA()
	id := KeyID(pubOf(a))
	sc := &Sidecar{KeyID: id}
	verified := Key{ID: id, Public: pubOf(a)}

	if err := CheckKeyIDAgreement(id, sc, verified); err != nil {
		t.Fatalf("all three agree: %v", err)
	}
	other := KeyID(pubOf(testKeyB()))
	for name, args := range map[string]struct {
		manifest string
		sc       *Sidecar
		key      Key
	}{
		"manifest differs": {other, sc, verified},
		"sidecar differs":  {id, &Sidecar{KeyID: other}, verified},
		"verified differs": {id, sc, Key{ID: other}},
		"empty manifest":   {"", sc, verified},
		"all empty":        {"", &Sidecar{}, Key{}},
	} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, CheckKeyIDAgreement(args.manifest, args.sc, args.key), CodeKeyIDMismatch)
		})
	}
}

func TestSignSidecarCanonicalForm(t *testing.T) {
	a := testKeyA()
	bundle := []byte("any bytes at all \x00\xff")
	out := SignSidecar(a, bundle)

	want := `{"sidecar_version":"1","algorithm":"ed25519","key_id":"` + KeyID(pubOf(a)) +
		`","signature":"` + base64.StdEncoding.EncodeToString(ed25519.Sign(a, bundle)) + `"}`
	if string(out) != want {
		t.Fatalf("sidecar =\n%s\nwant\n%s", out, want)
	}
	if bytes.HasSuffix(out, []byte("\n")) {
		t.Fatal("canonical sidecar has no trailing newline")
	}
	sc, err := ParseSidecar(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trustA(t).Verify(bundle, sc); err != nil {
		t.Fatal(err)
	}
	// Ed25519 is deterministic, so fixtures are reproducible.
	if !bytes.Equal(out, SignSidecar(a, bundle)) {
		t.Fatal("SignSidecar is not deterministic")
	}
}
