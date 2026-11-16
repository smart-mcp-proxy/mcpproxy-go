package tokenseal

import (
	"bytes"
	"regexp"
	"testing"
)

var envelopeRE = regexp.MustCompile(`^enc:v1:[0-9a-f]{16}:[A-Za-z0-9+/=]+$`)

func TestSealOpenRoundTrip(t *testing.T) {
	k := mustKey(t, 0)
	pt := []byte(`{"access_token":"abc","refresh_token":"def"}`)
	env, err := k.Seal("oauth_tokens", "srv_0123456789abcdef", pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !envelopeRE.Match(env) {
		t.Fatalf("envelope shape wrong: %q", env)
	}
	if bytes.Contains(env, []byte("abc")) || bytes.Contains(env, []byte("access_token")) {
		t.Fatalf("plaintext leaked into envelope")
	}
	got, err := k.Open("oauth_tokens", "srv_0123456789abcdef", env)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("round trip mismatch: %q", got)
	}
}

func TestSealFreshNoncePerSeal(t *testing.T) {
	k := mustKey(t, 0)
	pt := []byte(`{"a":1}`)
	seenEnv := map[string]bool{}
	seenNonce := map[string]bool{}
	for i := 0; i < 1000; i++ {
		env, err := k.Seal("b", "k", pt)
		if err != nil {
			t.Fatal(err)
		}
		if seenEnv[string(env)] {
			t.Fatalf("duplicate ciphertext at %d", i)
		}
		seenEnv[string(env)] = true
		// the first 16 base64 chars encode the first 12 bytes: the nonce.
		payload := string(env[len("enc:v1:")+17:])
		n := payload[:16]
		if seenNonce[n] {
			t.Fatalf("duplicate nonce at %d", i)
		}
		seenNonce[n] = true
	}
}

func TestSealEmptyPlaintext(t *testing.T) {
	k := mustKey(t, 0)
	env, err := k.Seal("b", "k", nil)
	if err != nil {
		t.Fatalf("empty plaintext must seal: %v", err)
	}
	got, err := k.Open("b", "k", env)
	if err != nil || len(got) != 0 {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestSealNilKey(t *testing.T) {
	var k *Key
	if _, err := k.Seal("b", "k", []byte("x")); err == nil {
		t.Fatal("Seal on a nil key must fail, not write plaintext")
	}
}

func TestClassify(t *testing.T) {
	k := mustKey(t, 0)
	env, _ := k.Seal("b", "k", []byte(`{}`))
	cases := []struct {
		name  string
		in    []byte
		shape Shape
		kid   string
	}{
		{"json", []byte(`{"a":1}`), ShapePlaintext, ""},
		{"sealed", env, ShapeSealedV1, k.ID()},
		{"v2", []byte("enc:v2:0123456789abcdef:AAAA"), ShapeUnsupported, ""},
		{"v10", []byte("enc:v10:0123456789abcdef:AAAA"), ShapeUnsupported, ""},
		{"empty", nil, ShapeMalformed, ""},
		{"empty slice", []byte{}, ShapeMalformed, ""},
		{"other", []byte("hello"), ShapeMalformed, ""},
		{"enc no version", []byte("enc:"), ShapeMalformed, ""},
		{"v1 short kid", []byte("enc:v1:abcd:AAAA"), ShapeMalformed, ""},
		{"v1 upper kid", []byte("enc:v1:0123456789ABCDEF:AAAA"), ShapeMalformed, ""},
		{"v1 no payload", []byte("enc:v1:0123456789abcdef:"), ShapeMalformed, ""},
		{"v1 no colon", []byte("enc:v1:0123456789abcdef"), ShapeMalformed, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, kid := Classify(c.in)
			if s != c.shape || kid != c.kid {
				t.Fatalf("Classify = (%v,%q), want (%v,%q)", s, kid, c.shape, c.kid)
			}
		})
	}
}
