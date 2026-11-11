package tokenseal

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseKey(t *testing.T) {
	raw, std := testIKM(0)
	rawNoPad := base64.RawStdEncoding.EncodeToString(raw)
	// bytes chosen so the URL alphabet differs from the standard one
	urlRaw := make([]byte, 32)
	for i := range urlRaw {
		urlRaw[i] = 0xfb
	}
	urlB64 := base64.URLEncoding.EncodeToString(urlRaw)
	if !strings.ContainsAny(urlB64, "-_") {
		t.Fatal("test vector must contain URL-only characters")
	}
	ok := []struct{ name, in string }{
		{"padded", std},
		{"unpadded", rawNoPad},
		{"surrounding space", "  " + std + "  "},
		{"trailing newline", std + "\n"},
		{"trailing crlf", std + "\r\n"},
	}
	for _, c := range ok {
		t.Run("ok/"+c.name, func(t *testing.T) {
			k, err := ParseKey(c.in)
			if err != nil || k == nil {
				t.Fatalf("ParseKey(%q): %v", c.in, err)
			}
			if k.ID() != mustKey(t, 0).ID() {
				t.Fatalf("kid differs")
			}
		})
	}
	bad := []struct{ name, in string }{
		{"31 bytes", base64.StdEncoding.EncodeToString(raw[:31])},
		{"33 bytes", base64.StdEncoding.EncodeToString(append(append([]byte{}, raw...), 1))},
		{"url alphabet", urlB64},
		{"empty", ""},
		{"whitespace only", " \n"},
		{"not base64", "this is not base64 at all!!"},
		{"hex", strings.Repeat("ab", 32)},
	}
	for _, c := range bad {
		t.Run("bad/"+c.name, func(t *testing.T) {
			k, err := ParseKey(c.in)
			if err == nil || k != nil {
				t.Fatalf("ParseKey(%q) must fail", c.in)
			}
			if in := strings.TrimSpace(c.in); in != "" && strings.Contains(err.Error(), in) {
				t.Fatalf("error echoes the input: %v", err)
			}
			if err.Error() != ErrInvalidKey.Error() {
				t.Fatalf("error must be the fixed message, got %q", err.Error())
			}
		})
	}
}

// Known-answer vectors computed independently (RFC 5869 HKDF-SHA256, no salt)
// for ikm = 00 01 02 ... 1f.
func TestDeriveKnownAnswer(t *testing.T) {
	ikm, _ := testIKM(0)
	rec, kid := derive(ikm)
	const wantRec = "370bf1e580df980c7cb661b954f0671ba703dc80f281cc7178b50b1a188379db"
	const wantKID = "cc5e139e47094daa"
	if fmt.Sprintf("%x", rec) != wantRec {
		t.Fatalf("record key = %x", rec)
	}
	if kid != wantKID {
		t.Fatalf("kid = %s", kid)
	}
}

// An envelope built by hand with the documented AAD layout must open, which
// pins the wire format independently of Seal.
func TestOpenHandBuiltEnvelope(t *testing.T) {
	ikm, _ := testIKM(0)
	rec, kid := derive(ikm)
	blk, _ := aes.NewCipher(rec)
	g, _ := cipher.NewGCM(blk)
	nonce := []byte("0123456789ab")
	aad := "mcpproxy-oauth-token\x00v1\x00" + kid + "\x00oauth_tokens\x00srv_0123456789abcdef"
	ct := g.Seal(nil, nonce, []byte(`{"k":"v"}`), []byte(aad))
	env := "enc:v1:" + kid + ":" + base64.StdEncoding.EncodeToString(append(append([]byte{}, nonce...), ct...))
	k := mustKey(t, 0)
	got, err := k.Open("oauth_tokens", "srv_0123456789abcdef", []byte(env))
	if err != nil || string(got) != `{"k":"v"}` {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestKeyDoesNotPrintMaterial(t *testing.T) {
	raw, b64 := testIKM(7)
	k, err := ParseKey(b64)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(k) //nolint:staticcheck // asserting that marshaling a Key leaks nothing
	out := fmt.Sprintf("%v|%+v|%#v|%s|%q", k, k, k, js, k)
	rec, _ := derive(raw)
	for _, secret := range []string{b64, strings.TrimRight(b64, "="), fmt.Sprintf("%x", raw), fmt.Sprintf("%x", rec),
		base64.StdEncoding.EncodeToString(rec), string(raw), string(rec)} {
		if strings.Contains(out, secret) {
			t.Fatalf("formatted key contains secret material %q in %q", secret, out)
		}
	}
}
