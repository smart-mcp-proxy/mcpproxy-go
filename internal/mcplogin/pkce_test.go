package mcplogin

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestVerifyS256RFCVector(t *testing.T) {
	v := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	c := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !VerifyS256(v, c) {
		t.Fatal("RFC vector must verify")
	}
	if !ValidChallenge(c) {
		t.Fatal("challenge must be valid")
	}
}

func TestVerifyS256Rejects(t *testing.T) {
	v := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	c := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if VerifyS256(v+"x", c) {
		t.Error("wrong verifier")
	}
	if VerifyS256(v, c[:42]+"A") {
		t.Error("wrong challenge")
	}
	if VerifyS256(strings.Repeat("a", 42), challengeOf(strings.Repeat("a", 42))) ||
		VerifyS256(strings.Repeat("a", 129), challengeOf(strings.Repeat("a", 129))) {
		t.Error("length bounds")
	}
	if VerifyS256(v[:42]+"!", challengeOf(v[:42]+"!")) || VerifyS256(v[:42]+" ", challengeOf(v[:42]+" ")) {
		t.Error("bad chars")
	}
	if VerifyS256("", "") {
		t.Error("empty")
	}
}

func TestVerifyS256Bounds(t *testing.T) {
	for _, n := range []int{43, 128} {
		v := strings.Repeat("a", n)
		if !VerifyS256(v, challengeOf(v)) {
			t.Errorf("length %d should be valid", n)
		}
	}
	v := "a-._~" + strings.Repeat("Z", 40)
	if !VerifyS256(v, challengeOf(v)) {
		t.Error("unreserved set")
	}
}

func TestValidChallenge(t *testing.T) {
	good := strings.Repeat("A", 43)
	if !ValidChallenge(good) {
		t.Error("good")
	}
	for _, bad := range []string{"", good[:42], good + "A", good[:42] + "=", good[:42] + "+", good[:42] + "/"} {
		if ValidChallenge(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
