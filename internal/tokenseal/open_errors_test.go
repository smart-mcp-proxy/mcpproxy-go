package tokenseal

import (
	"errors"
	"strings"
	"testing"
)

func reasonOf(t *testing.T, err error) *OpenError {
	t.Helper()
	var oe *OpenError
	if !errors.As(err, &oe) {
		t.Fatalf("want *OpenError, got %T %v", err, err)
	}
	return oe
}

func TestOpenErrorReasons(t *testing.T) {
	k := mustKey(t, 0)
	other := mustKey(t, 1)
	const bucket, bkey = "oauth_tokens", "srv_0123456789abcdef"
	env, err := k.Seal(bucket, bkey, []byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := string(env[len("enc:v1:")+17:])

	t.Run("nil key", func(t *testing.T) {
		var nk *Key
		_, err := nk.Open(bucket, bkey, env)
		oe := reasonOf(t, err)
		if oe.Reason != ReasonNoKey || oe.RowKID != k.ID() || oe.KeyKID != "" {
			t.Fatalf("got %+v", oe)
		}
	})
	t.Run("other key", func(t *testing.T) {
		_, err := other.Open(bucket, bkey, env)
		oe := reasonOf(t, err)
		if oe.Reason != ReasonKeyMismatch || oe.RowKID != k.ID() || oe.KeyKID != other.ID() {
			t.Fatalf("got %+v", oe)
		}
	})
	t.Run("prefix kid edited to unrelated kid", func(t *testing.T) {
		edited := []byte("enc:v1:0000000000000000:" + payload)
		_, err := k.Open(bucket, bkey, edited)
		oe := reasonOf(t, err)
		if oe.Reason != ReasonKeyMismatch || oe.RowKID != "0000000000000000" || oe.KeyKID != k.ID() {
			t.Fatalf("got %+v", oe)
		}
	})
	t.Run("kid edited to configured kid under other key", func(t *testing.T) {
		// Sealed by other, prefix rewritten to k's kid: no kid mismatch, so the
		// decrypt is attempted and authentication fails.
		oenv, _ := other.Seal(bucket, bkey, []byte(`{"x":1}`))
		p := string(oenv[len("enc:v1:")+17:])
		edited := []byte("enc:v1:" + k.ID() + ":" + p)
		_, err := k.Open(bucket, bkey, edited)
		if reasonOf(t, err).Reason != ReasonAuthFailed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("other bucket key", func(t *testing.T) {
		_, err := k.Open(bucket, "srv_ffffffffffffffff", env)
		if reasonOf(t, err).Reason != ReasonAuthFailed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("other bucket name", func(t *testing.T) {
		_, err := k.Open("other_bucket", bkey, env)
		if reasonOf(t, err).Reason != ReasonAuthFailed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("flipped ciphertext byte", func(t *testing.T) {
		b := append([]byte{}, env...)
		i := len(b) - 6
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		_, err := k.Open(bucket, bkey, b)
		if reasonOf(t, err).Reason != ReasonAuthFailed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("truncated payload", func(t *testing.T) {
		_, err := k.Open(bucket, bkey, env[:len("enc:v1:")+17+8])
		if reasonOf(t, err).Reason != ReasonMalformed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad base64", func(t *testing.T) {
		_, err := k.Open(bucket, bkey, []byte("enc:v1:"+k.ID()+":!!!!not-base64!!!!"))
		if reasonOf(t, err).Reason != ReasonMalformed {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("v2", func(t *testing.T) {
		_, err := k.Open(bucket, bkey, []byte("enc:v2:"+k.ID()+":AAAA"))
		if reasonOf(t, err).Reason != ReasonUnsupportedVersion {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not an envelope", func(t *testing.T) {
		for _, v := range [][]byte{nil, []byte("hello"), []byte(`{"a":1}`)} {
			_, err := k.Open(bucket, bkey, v)
			if reasonOf(t, err).Reason != ReasonMalformed {
				t.Fatalf("value %q: got %v", v, err)
			}
		}
	})
	t.Run("error text has no payload", func(t *testing.T) {
		_, err := other.Open(bucket, bkey, env)
		if strings.Contains(err.Error(), payload) {
			t.Fatalf("error leaks payload: %v", err)
		}
		if !strings.Contains(err.Error(), k.ID()) || !strings.Contains(err.Error(), other.ID()) {
			t.Fatalf("error should name both kids: %v", err)
		}
	})
}
