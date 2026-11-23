package mcplogin

import (
	"strings"
	"testing"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestAccessToken(t *testing.T) {
	a, err := NewAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, AccessTokenPrefix) || len(a) != len(AccessTokenPrefix)+43 {
		t.Fatalf("bad access token %q", a)
	}
	if !IsAccessToken(a) || IsRefreshToken(a) {
		t.Fatal("prefix checks wrong")
	}
	b, _ := NewAccessToken()
	if a == b {
		t.Fatal("tokens must differ")
	}
}

func TestGrantID(t *testing.T) {
	g, err := NewGrantID()
	if err != nil || len(g) != 22 {
		t.Fatalf("grant id %q %v", g, err)
	}
	for _, c := range g {
		if !strings.ContainsRune(base62Alphabet, c) {
			t.Fatalf("non base62 %q", g)
		}
	}
}

func TestRefreshTokenRoundTrip(t *testing.T) {
	g, _ := NewGrantID()
	tok, secret, err := NewRefreshToken(testKey, g)
	if err != nil {
		t.Fatal(err)
	}
	if !IsRefreshToken(tok) || IsAccessToken(tok) {
		t.Fatal("prefix checks wrong")
	}
	if len(tok) != len(RefreshTokenPrefix)+22+1+43+1+22 {
		t.Fatalf("length %d", len(tok))
	}
	gid, sec, ok := ParseRefreshToken(testKey, tok)
	if !ok || gid != g || sec != secret {
		t.Fatalf("round trip failed: %v %q %q", ok, gid, sec)
	}
	tok2, secret2, _ := NewRefreshToken(testKey, g)
	if tok2 == tok || secret2 == secret {
		t.Fatal("two tokens of one grant must differ")
	}
}

func TestNewRefreshTokenRejectsBadGrantID(t *testing.T) {
	if _, _, err := NewRefreshToken(testKey, "short"); err == nil {
		t.Fatal("bad grant id accepted")
	}
	if _, _, err := NewRefreshToken(nil, strings.Repeat("a", 22)); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestParseRefreshTokenRejects(t *testing.T) {
	g, _ := NewGrantID()
	tok, _, _ := NewRefreshToken(testKey, g)
	other := []byte("ffffffffffffffffffffffffffffffff")
	otherTok, _, _ := NewRefreshToken(other, g)
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	p := len(RefreshTokenPrefix)
	cases := map[string]string{
		"empty":        "",
		"wrong prefix": "mcp_oat_" + tok[p:],
		"short":        tok[:len(tok)-1],
		"long":         tok + "A",
		"grant":        flip(tok, p+3),
		"secret":       flip(tok, p+22+1+5),
		"mac":          flip(tok, len(tok)-3),
		"other key":    otherTok,
		"no seps":      strings.ReplaceAll(tok, "_", "-"),
	}
	for name, c := range cases {
		if _, _, ok := ParseRefreshToken(testKey, c); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, ok := ParseRefreshToken(nil, tok); ok {
		t.Error("nil key accepted")
	}
}

func TestHashSecretKinds(t *testing.T) {
	s := "secret"
	hs := map[string]bool{}
	for _, k := range []string{"access", "refresh", "code"} {
		h := HashSecret(testKey, k, s)
		if len(h) != 64 || hs[h] {
			t.Fatalf("kind %s: bad or colliding hash", k)
		}
		hs[h] = true
		if h != HashSecret(testKey, k, s) {
			t.Fatal("not deterministic")
		}
	}
	if HashSecret(testKey, "access", s) == HashSecret(testKey, "access", s+"x") {
		t.Fatal("collision")
	}
}

func TestSecretEqualAndPrefix(t *testing.T) {
	if !SecretEqual("a", "a") || SecretEqual("a", "b") || SecretEqual("a", "ab") {
		t.Fatal("SecretEqual wrong")
	}
	if DisplayPrefix("mcp_oat_abcdefgh") != "mcp_oat_abcd" || DisplayPrefix("abc") != "abc" {
		t.Fatal("DisplayPrefix wrong")
	}
}
