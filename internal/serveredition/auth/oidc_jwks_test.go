//go:build server

package auth

// Spec 107 T036 (PR-B): stdlib JWK → public key (research D3, FR-020).
//
// Contract exercised here, implemented by T043 in oidc_jwks.go (≤150 lines):
//
//	type jwk struct{ Kty, Kid, Use, Alg, N, E, Crv, X, Y string } // json tags: kty,kid,use,alg,n,e,crv,x,y
//	func jwkToPublicKey(k jwk) (crypto.PublicKey, error)           // RSA (n,e) and EC (crv,x,y) only
//	func parseJWKS(data []byte) (map[string]crypto.PublicKey, error) // {"keys":[...]} → kid → key
//
// jwkToPublicKey refuses (returns an error and a nil key) every key it cannot
// turn into an *rsa.PublicKey or an *ecdsa.PublicKey: unknown/absent kty,
// malformed or absent n/e/x/y, unknown crv, an EC point off its curve.
// parseJWKS refuses a document that is not a JSON object carrying a keys array
// and otherwise skips — never fails on — individual entries jwkToPublicKey
// refuses or that carry no kid, so one exotic key in a real IdP's set cannot
// take every other key with it; a skipped kid is simply absent from the map,
// which the provider then treats as an unknown kid (one refetch, then refuse).

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

var (
	jwksFixtureOnce sync.Once
	jwksFixtureRSA  *rsa.PrivateKey
	jwksFixtureEC   map[string]*ecdsa.PrivateKey // by crv name
)

// jwksFixtureKeys generates the test key material once per package run.
func jwksFixtureKeys(t *testing.T) (*rsa.PrivateKey, map[string]*ecdsa.PrivateKey) {
	t.Helper()
	jwksFixtureOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		jwksFixtureRSA = k
		jwksFixtureEC = map[string]*ecdsa.PrivateKey{}
		for name, curve := range map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()} {
			ek, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				panic(err)
			}
			jwksFixtureEC[name] = ek
		}
	})
	return jwksFixtureRSA, jwksFixtureEC
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// rsaJWK encodes a public RSA key the way RFC 7518 §6.3 and every real IdP do.
func rsaJWK(kid string, pub *rsa.PublicKey) jwk {
	return jwk{
		Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
		N: b64url(pub.N.Bytes()),
		E: b64url(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// ecJWK encodes a public EC key with fixed-width coordinates (RFC 7518 §6.2.1).
func ecJWK(kid, crv string, pub *ecdsa.PublicKey) jwk {
	size := (pub.Curve.Params().BitSize + 7) / 8
	return jwk{
		Kty: "EC", Kid: kid, Use: "sig", Crv: crv,
		X: b64url(pub.X.FillBytes(make([]byte, size))),
		Y: b64url(pub.Y.FillBytes(make([]byte, size))),
	}
}

func jwksDoc(t *testing.T, keys ...jwk) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"keys": keys})
	require.NoError(t, err)
	return data
}

func TestJWKToPublicKey_RSA(t *testing.T) {
	priv, _ := jwksFixtureKeys(t)

	key, err := jwkToPublicKey(rsaJWK("k1", &priv.PublicKey))
	require.NoError(t, err)

	pub, ok := key.(*rsa.PublicKey)
	require.True(t, ok, "RSA JWK must become *rsa.PublicKey, got %T", key)
	assert.True(t, pub.Equal(&priv.PublicKey), "parsed key must equal the source public key")
	assert.Equal(t, 65537, pub.E)
}

func TestJWKToPublicKey_RSA_AlgAndUseAreNotRequired(t *testing.T) {
	// Entra publishes keys without "alg"; Keycloak publishes "use": "enc" keys
	// beside signing keys. Neither field gates the conversion — the algorithm
	// is decided by the verifier's allowed-alg intersection (research D3).
	priv, _ := jwksFixtureKeys(t)
	k := rsaJWK("k1", &priv.PublicKey)
	k.Alg, k.Use = "", ""

	key, err := jwkToPublicKey(k)
	require.NoError(t, err)
	require.IsType(t, &rsa.PublicKey{}, key)
}

func TestJWKToPublicKey_EC(t *testing.T) {
	_, ecKeys := jwksFixtureKeys(t)

	for crv, priv := range ecKeys {
		t.Run(crv, func(t *testing.T) {
			key, err := jwkToPublicKey(ecJWK("e1", crv, &priv.PublicKey))
			require.NoError(t, err)

			pub, ok := key.(*ecdsa.PublicKey)
			require.True(t, ok, "EC JWK must become *ecdsa.PublicKey, got %T", key)
			assert.True(t, pub.Equal(&priv.PublicKey), "parsed key must equal the source public key")
			assert.Equal(t, priv.Curve.Params().Name, pub.Curve.Params().Name)
		})
	}
}

func TestJWKToPublicKey_UnknownKtyRefused(t *testing.T) {
	priv, ecKeys := jwksFixtureKeys(t)

	cases := map[string]jwk{
		"oct symmetric":     {Kty: "oct", Kid: "s1", Alg: "HS256", N: b64url([]byte("secret"))},
		"OKP":               {Kty: "OKP", Kid: "o1", Crv: "Ed25519", X: b64url(make([]byte, 32))},
		"empty kty":         {Kid: "k1", N: rsaJWK("k1", &priv.PublicKey).N, E: "AQAB"},
		"lower-case rsa":    {Kty: "rsa", Kid: "k1", N: rsaJWK("k1", &priv.PublicKey).N, E: "AQAB"},
		"lower-case ec":     func() jwk { k := ecJWK("e1", "P-256", &ecKeys["P-256"].PublicKey); k.Kty = "ec"; return k }(),
		"kty with EC body":  func() jwk { k := ecJWK("e1", "P-256", &ecKeys["P-256"].PublicKey); k.Kty = "RSA"; return k }(),
		"kty with RSA body": func() jwk { k := rsaJWK("k1", &priv.PublicKey); k.Kty = "EC"; return k }(),
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := jwkToPublicKey(k)
			require.Error(t, err)
			assert.Nil(t, key, "a refused JWK must not yield a key")
		})
	}
}

func TestJWKToPublicKey_MalformedRSARefused(t *testing.T) {
	priv, _ := jwksFixtureKeys(t)
	good := rsaJWK("k1", &priv.PublicKey)

	mutate := func(f func(k *jwk)) jwk { k := good; f(&k); return k }
	cases := map[string]jwk{
		"n absent":                 mutate(func(k *jwk) { k.N = "" }),
		"e absent":                 mutate(func(k *jwk) { k.E = "" }),
		"n not base64url":          mutate(func(k *jwk) { k.N = "!!not-base64!!" }),
		"e not base64url":          mutate(func(k *jwk) { k.E = "!!" }),
		"n with std-base64 chars":  mutate(func(k *jwk) { k.N = "ab+/cd" }),
		"e with padding":           mutate(func(k *jwk) { k.E = "AQAB=" }),
		"e zero":                   mutate(func(k *jwk) { k.E = b64url([]byte{0}) }),
		"e one":                    mutate(func(k *jwk) { k.E = b64url([]byte{1}) }),
		"e overflows int":          mutate(func(k *jwk) { k.E = b64url([]byte{1, 0, 0, 0, 0, 0, 0, 0, 1}) }),
		"n zero":                   mutate(func(k *jwk) { k.N = b64url([]byte{0}) }),
		"n and e both empty bytes": mutate(func(k *jwk) { k.N = b64url(nil); k.E = b64url(nil) }),
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := jwkToPublicKey(k)
			require.Error(t, err)
			assert.Nil(t, key, "a refused JWK must not yield a key")
		})
	}
}

func TestJWKToPublicKey_MalformedECRefused(t *testing.T) {
	_, ecKeys := jwksFixtureKeys(t)
	p256 := ecKeys["P-256"]
	good := ecJWK("e1", "P-256", &p256.PublicKey)

	mutate := func(f func(k *jwk)) jwk { k := good; f(&k); return k }

	// A point that is not on P-256: keep x, flip one bit of y.
	offCurveY := p256.PublicKey.Y.FillBytes(make([]byte, 32))
	offCurveY[31] ^= 0x01

	cases := map[string]jwk{
		"crv absent":            mutate(func(k *jwk) { k.Crv = "" }),
		"crv unknown":           mutate(func(k *jwk) { k.Crv = "secp256k1" }),
		"crv mismatch for size": mutate(func(k *jwk) { k.Crv = "P-384" }),
		"x absent":              mutate(func(k *jwk) { k.X = "" }),
		"y absent":              mutate(func(k *jwk) { k.Y = "" }),
		"x not base64url":       mutate(func(k *jwk) { k.X = "!!" }),
		"y not base64url":       mutate(func(k *jwk) { k.Y = "!!" }),
		"point off curve":       mutate(func(k *jwk) { k.Y = b64url(offCurveY) }),
		"x,y zero":              mutate(func(k *jwk) { k.X = b64url(make([]byte, 32)); k.Y = b64url(make([]byte, 32)) }),
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := jwkToPublicKey(k)
			require.Error(t, err)
			assert.Nil(t, key, "a refused JWK must not yield a key")
		})
	}
}

func TestParseJWKS_MapsByKid(t *testing.T) {
	priv, ecKeys := jwksFixtureKeys(t)

	keys, err := parseJWKS(jwksDoc(t,
		rsaJWK("rsa-1", &priv.PublicKey),
		ecJWK("ec-1", "P-256", &ecKeys["P-256"].PublicKey),
	))
	require.NoError(t, err)
	require.Len(t, keys, 2)

	rsaPub, ok := keys["rsa-1"].(*rsa.PublicKey)
	require.True(t, ok, "rsa-1 must be *rsa.PublicKey, got %T", keys["rsa-1"])
	assert.True(t, rsaPub.Equal(&priv.PublicKey))

	ecPub, ok := keys["ec-1"].(*ecdsa.PublicKey)
	require.True(t, ok, "ec-1 must be *ecdsa.PublicKey, got %T", keys["ec-1"])
	assert.True(t, ecPub.Equal(&ecKeys["P-256"].PublicKey))
}

func TestParseJWKS_SkipsRefusedEntriesKeepsTheRest(t *testing.T) {
	priv, ecKeys := jwksFixtureKeys(t)
	badRSA := rsaJWK("rsa-bad", &priv.PublicKey)
	badRSA.E = "!!"

	keys, err := parseJWKS(jwksDoc(t,
		jwk{Kty: "oct", Kid: "sym", N: b64url([]byte("secret"))}, // unknown kty
		badRSA,                                  // malformed e
		jwk{Kty: "RSA", N: badRSA.N, E: "AQAB"}, // no kid → unaddressable
		rsaJWK("rsa-good", &priv.PublicKey),
		ecJWK("ec-good", "P-256", &ecKeys["P-256"].PublicKey),
	))
	require.NoError(t, err, "one refused entry must not take the whole set with it")

	assert.Len(t, keys, 2)
	assert.Contains(t, keys, "rsa-good")
	assert.Contains(t, keys, "ec-good")
	assert.NotContains(t, keys, "sym")
	assert.NotContains(t, keys, "rsa-bad")
	assert.NotContains(t, keys, "")
}

func TestParseJWKS_EmptySet(t *testing.T) {
	keys, err := parseJWKS([]byte(`{"keys":[]}`))
	require.NoError(t, err)
	assert.Empty(t, keys)
	assert.NotNil(t, keys, "an empty set is an empty map, not nil")
}

func TestParseJWKS_MalformedDocumentRefused(t *testing.T) {
	cases := map[string]string{
		"not json":            `{"keys":[`,
		"empty body":          ``,
		"json null":           `null`,
		"array not object":    `[{"kty":"RSA"}]`,
		"keys missing":        `{"kid":"x"}`,
		"keys is object":      `{"keys":{"kty":"RSA"}}`,
		"keys is string":      `{"keys":"RSA"}`,
		"key entry is string": `{"keys":["RSA"]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			keys, err := parseJWKS([]byte(doc))
			require.Error(t, err)
			assert.Nil(t, keys)
		})
	}
}

func TestParseJWKS_IgnoresUnknownFields(t *testing.T) {
	// Real sets carry x5c, x5t, x5t#S256, issuer, cloud_instance_name, … .
	priv, _ := jwksFixtureKeys(t)
	k := rsaJWK("rsa-1", &priv.PublicKey)
	doc := `{"keys":[{"kty":"RSA","kid":"rsa-1","use":"sig","alg":"RS256","n":"` + k.N + `","e":"` + k.E + `",` +
		`"x5c":["MIIC"],"x5t":"abc","x5t#S256":"def","issuer":"https://login.example.com","cloud_instance_name":"x"}],` +
		`"extra":{"nested":true}}`

	keys, err := parseJWKS([]byte(doc))
	require.NoError(t, err)
	require.Contains(t, keys, "rsa-1")
	assert.True(t, keys["rsa-1"].(*rsa.PublicKey).Equal(&priv.PublicKey))
}

func TestParseJWKS_DuplicateKidLastWins(t *testing.T) {
	// Not a real-IdP shape, but the cache is a map: the behaviour must be
	// deterministic rather than an error that would refuse every login.
	priv, ecKeys := jwksFixtureKeys(t)

	keys, err := parseJWKS(jwksDoc(t,
		rsaJWK("dup", &priv.PublicKey),
		ecJWK("dup", "P-256", &ecKeys["P-256"].PublicKey),
	))
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.IsType(t, &ecdsa.PublicKey{}, keys["dup"])
}

func TestParseJWKS_AgainstFakeIdPKeyRing(t *testing.T) {
	// The exact document tests/oauthserver serves on /jwks.json (T033):
	// RSA keys from the rotating ring plus the EC key the
	// IDTokenKeyAlgMismatch knob publishes. Every kid must resolve to the
	// ring's own public key, both before and after a rotation.
	ring, err := oauthserver.NewKeyRing()
	require.NoError(t, err)
	ecKid, err := ring.AddECKey()
	require.NoError(t, err)
	rotatedKid, err := ring.RotateKey()
	require.NoError(t, err)

	doc, err := json.Marshal(ring.GetJWKS())
	require.NoError(t, err)

	keys, err := parseJWKS(doc)
	require.NoError(t, err)
	require.Len(t, keys, 3, "key-1, the rotated key and the EC key")

	for _, kid := range []string{"key-1", rotatedKid} {
		priv, ok := ring.GetKey(kid)
		require.True(t, ok)
		got, ok := keys[kid].(*rsa.PublicKey)
		require.True(t, ok, "%s must be *rsa.PublicKey, got %T", kid, keys[kid])
		assert.True(t, got.Equal(&priv.PublicKey), "kid %s", kid)
	}

	gotEC, ok := keys[ecKid].(*ecdsa.PublicKey)
	require.True(t, ok, "%s must be *ecdsa.PublicKey, got %T", ecKid, keys[ecKid])
	_, ecPriv := ring.GetECKey()
	assert.True(t, gotEC.Equal(&ecPriv.PublicKey))
}

// Compile-time pin of the contract T043 must satisfy (research D3 /
// data-model §7: jwksCache is map[kid]crypto.PublicKey).
var (
	_ func(jwk) (crypto.PublicKey, error)               = jwkToPublicKey
	_ func([]byte) (map[string]crypto.PublicKey, error) = parseJWKS
)
