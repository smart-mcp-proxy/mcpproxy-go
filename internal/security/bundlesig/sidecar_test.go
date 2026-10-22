package bundlesig

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want rejection %q, got nil error", code)
	}
	if got := CodeOf(err); got != code {
		t.Fatalf("want code %q, got %q (%v)", code, got, err)
	}
}

func TestParseSidecarValid(t *testing.T) {
	a := testKeyA()
	for _, name := range []string{"valid.json.sig", "accept-trailing-newline.sig", "accept-4096-bytes.sig", "accept-all-zero-signature.sig"} {
		t.Run(name, func(t *testing.T) {
			sc, err := ParseSidecar(readFixture(t, name))
			if err != nil {
				t.Fatalf("ParseSidecar: %v", err)
			}
			if sc.Version != "1" || sc.Algorithm != "ed25519" {
				t.Fatalf("version/algorithm = %q/%q", sc.Version, sc.Algorithm)
			}
			if sc.KeyID != KeyID(pubOf(a)) {
				t.Fatalf("key id = %q", sc.KeyID)
			}
			if len(sc.Signature) != ed25519.SignatureSize {
				t.Fatalf("signature len = %d", len(sc.Signature))
			}
		})
	}
	sc, err := ParseSidecar(readFixture(t, "accept-all-zero-signature.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sc.Signature, make([]byte, 64)) {
		t.Fatal("all-zero fixture did not decode to 64 zero bytes")
	}
}

// TestParseSidecarRejectFixtures pins one fixture per contract row 1-10; the
// assertion is on the reason code, never merely "it failed".
func TestParseSidecarRejectFixtures(t *testing.T) {
	cases := []string{
		CodeSidecarTooLarge, CodeSidecarMalformed, CodeSidecarMissingField,
		CodeSidecarUnknownField, CodeSidecarDuplicateField, CodeSidecarType,
		CodeSidecarVersionUnsupported, CodeSidecarAlgorithmUnsupported,
		CodeSidecarKeyIDMalformed, CodeSidecarSignatureMalformed,
	}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			_, err := ParseSidecar(readFixture(t, "reject-"+code+".sig"))
			wantCode(t, err, code)
		})
	}
}

func TestParseSidecarSizeBoundary(t *testing.T) {
	good := readFixture(t, "valid.json.sig")
	at := append(append([]byte{}, good...), bytes.Repeat([]byte("\n"), MaxSidecarBytes-len(good))...)
	if len(at) != 4096 {
		t.Fatalf("setup: len = %d", len(at))
	}
	if _, err := ParseSidecar(at); err != nil {
		t.Fatalf("exactly 4096 bytes must parse: %v", err)
	}
	_, err := ParseSidecar(append(at, ' '))
	wantCode(t, err, CodeSidecarTooLarge)
	// The cap is checked before parsing: 4097 bytes of garbage is too_large,
	// not malformed.
	_, err = ParseSidecar(bytes.Repeat([]byte("x"), MaxSidecarBytes+1))
	wantCode(t, err, CodeSidecarTooLarge)
}

// TestParseSidecarRowPrecedence: rows are evaluated in numerical order and the
// first match wins, so overlapping conditions report one stable reason.
func TestParseSidecarRowPrecedence(t *testing.T) {
	id := KeyID(pubOf(testKeyA()))
	cases := []struct {
		name string
		in   string
		code string
	}{
		{"unknown and missing -> missing (3 before 4)", `{"foo":1}`, CodeSidecarMissingField},
		{"empty object -> missing", `{}`, CodeSidecarMissingField},
		{"case-different key is a different key", `{"sidecar_version":"1","Algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarMissingField},
		{"duplicate and unknown -> unknown (4 before 5)", `{"sidecar_version":"1","algorithm":"ed25519","algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + `","x":1}`, CodeSidecarUnknownField},
		{"duplicate and wrong type -> duplicate (5 before 6)", `{"sidecar_version":"1","algorithm":"ed25519","algorithm":7,"key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarDuplicateField},
		{"escaped spelling of a key still duplicates", `{"sidecar_version":"1","algorithm":"ed25519","algorithm":"rsa","key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarDuplicateField},
		{"wrong type and bad version -> type (6 before 7)", `{"sidecar_version":"9","algorithm":7,"key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarType},
		{"null value is a type error", `{"sidecar_version":null,"algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarType},
		{"object value is a type error", `{"sidecar_version":"1","algorithm":{},"key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarType},
		{"bad version and bad algorithm -> version (7 before 8)", `{"sidecar_version":"2","algorithm":"rsa","key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarVersionUnsupported},
		{"bad algorithm and bad key id -> algorithm (8 before 9)", `{"sidecar_version":"1","algorithm":"rsa","key_id":"zz","signature":"` + zeroSig + `"}`, CodeSidecarAlgorithmUnsupported},
		{"bad key id and bad signature -> key id (9 before 10)", `{"sidecar_version":"1","algorithm":"ed25519","key_id":"zz","signature":"x"}`, CodeSidecarKeyIDMalformed},
		{"empty input", ``, CodeSidecarMalformed},
		{"whitespace only", " \n", CodeSidecarMalformed},
		{"array", `[]`, CodeSidecarMalformed},
		{"null", `null`, CodeSidecarMalformed},
		{"two objects", `{"a":1}{"b":2}`, CodeSidecarMalformed},
		{"truncated", `{"sidecar_version":"1"`, CodeSidecarMalformed},
		{"BOM", "\xef\xbb\xbf" + `{"sidecar_version":"1","algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + `"}`, CodeSidecarMalformed},
		{"invalid UTF-8", `{"sidecar_version":"1","algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + "\xff" + `"}`, CodeSidecarMalformed},
		{"trailing comma", `{"sidecar_version":"1","algorithm":"ed25519","key_id":"` + id + `","signature":"` + zeroSig + `",}`, CodeSidecarMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSidecar([]byte(tc.in))
			wantCode(t, err, tc.code)
		})
	}
}

func TestParseSidecarKeyIDGrammar(t *testing.T) {
	good := KeyID(pubOf(testKeyA()))
	bad := map[string]string{
		"uppercase":  strings.ToUpper(good),
		"63 chars":   good[:63],
		"65 chars":   good + "0",
		"non-hex":    "g" + good[1:],
		"empty":      "",
		"whitespace": " " + good[1:],
	}
	for name, id := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSidecar(strSidecar("1", "ed25519", id, zeroSig))
			wantCode(t, err, CodeSidecarKeyIDMalformed)
		})
	}
}

func TestParseSidecarSignatureEncoding(t *testing.T) {
	id := KeyID(pubOf(testKeyA()))
	rawSig := make([]byte, 64)
	for i := range rawSig {
		rawSig[i] = 0xfb // encodes to '-'/'_' under the URL alphabet
	}
	urlSig := base64.URLEncoding.EncodeToString(rawSig)
	if !strings.ContainsAny(urlSig, "-_") || len(urlSig) != 88 {
		t.Fatalf("setup: urlSig = %q", urlSig)
	}

	// 64 zero bytes end in "AA=="; the second-to-last char carries 4 padding
	// bits that must be zero. "AB==" decodes leniently but is not canonical.
	nonCanonical := zeroSig[:85] + "B==" // 85 + 3 = 88
	if len(nonCanonical) != 88 {
		t.Fatalf("setup: len = %d", len(nonCanonical))
	}
	if _, err := base64.StdEncoding.DecodeString(nonCanonical); err != nil {
		t.Fatalf("setup: lenient decoder should accept the non-canonical form: %v", err)
	}

	cases := map[string]string{
		"non-canonical trailing bits": nonCanonical,
		"87 chars":                    zeroSig[:87],
		"89 chars":                    zeroSig + "A",
		"url alphabet":                urlSig,
		"missing padding (86 chars)":  base64.RawStdEncoding.EncodeToString(make([]byte, 64)),
		"empty":                       "",
		"one char":                    "A",
		// 88 chars, but a newline replaces a data char: Strict() skips the
		// newline, so only the decoded length / round trip can catch it.
		"embedded newline (88 chars)": zeroSig[:10] + `\n` + zeroSig[11:],
		// Strict() ACCEPTS this (it skips the newline and decodes all 64
		// bytes); only the exact-88-chars rule rejects it.
		"embedded newline (89 chars, strict-decodable)": zeroSig[:10] + `\n` + zeroSig[10:],
		"embedded carriage return":                      zeroSig[:20] + `\r` + zeroSig[21:],
		"spaces":                                        strings.Repeat(" ", 88),
	}
	for name, sig := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSidecar(rawSidecar(`"1"`, `"ed25519"`, quote(id), quote(sig)))
			wantCode(t, err, CodeSidecarSignatureMalformed)
		})
	}

	sc, err := ParseSidecar(rawSidecar(`"1"`, `"ed25519"`, quote(id), quote(zeroSig)))
	if err != nil || len(sc.Signature) != 64 {
		t.Fatalf("control: %v", err)
	}
}

func TestParseSidecarWhitespaceAndOrderTolerated(t *testing.T) {
	a := testKeyA()
	bundle := readFixture(t, "valid.json")
	good, err := ParseSidecar(readFixture(t, "valid.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(good.Signature)
	variant := "\n  {\n\t\"signature\" : \"" + sigB64 + "\",\n  \"key_id\":\"" + good.KeyID +
		"\" ,\"algorithm\":\"ed25519\", \"sidecar_version\": \"1\"\n}  \r\n"
	sc, err := ParseSidecar([]byte(variant))
	if err != nil {
		t.Fatalf("reordered/whitespace variant must parse: %v", err)
	}
	ts, err := NewTrustSet(Key{ID: KeyID(pubOf(a)), Public: pubOf(a), Authority: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Verify(bundle, sc); err != nil {
		t.Fatalf("variant must still verify (SC-001 non-requirement): %v", err)
	}
}

func TestErrorAndCodeOf(t *testing.T) {
	e := &Error{Code: CodeSignatureInvalid, Detail: "boom"}
	if !strings.Contains(e.Error(), CodeSignatureInvalid) || !strings.Contains(e.Error(), "boom") {
		t.Fatalf("Error() = %q", e.Error())
	}
	wrapped := errors.Join(errors.New("ctx"), e)
	if CodeOf(wrapped) != CodeSignatureInvalid {
		t.Fatal("CodeOf must see through wrapping")
	}
	if CodeOf(errors.New("plain")) != "" || CodeOf(nil) != "" {
		t.Fatal("CodeOf of non-bundlesig error must be empty")
	}
}
