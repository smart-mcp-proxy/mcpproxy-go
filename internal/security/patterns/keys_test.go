package patterns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test RSA Private Key detection
func TestRSAPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "RSA private key header",
			input:     "-----BEGIN RSA PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name: "full RSA key block",
			input: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA0Z3VS5JJcds3xfn/ygWyF8PbnGy0AHB7MdU...
-----END RSA PRIVATE KEY-----`,
			wantMatch: true,
		},
		{
			name:      "RSA key in JSON",
			input:     `{"private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIEpA..."}`,
			wantMatch: true,
		},
		{
			name:      "public key (should not match)",
			input:     "-----BEGIN RSA PUBLIC KEY-----",
			wantMatch: false,
		},
		{
			name:      "random text",
			input:     "this is not a key",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	rsaPattern := findPatternByName(patterns, "rsa_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rsaPattern == nil {
				t.Skip("RSA private key pattern not implemented yet")
				return
			}
			matches := rsaPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test EC Private Key detection
func TestECPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "EC private key header",
			input:     "-----BEGIN EC PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name: "full EC key block",
			input: `-----BEGIN EC PRIVATE KEY-----
MHQCAQEEICg7E4NN+5sCiXwKj4bYdED7fDp3YdxbrQ...
-----END EC PRIVATE KEY-----`,
			wantMatch: true,
		},
		{
			name:      "EC public key (should not match)",
			input:     "-----BEGIN EC PUBLIC KEY-----",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	ecPattern := findPatternByName(patterns, "ec_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ecPattern == nil {
				t.Skip("EC private key pattern not implemented yet")
				return
			}
			matches := ecPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test DSA Private Key detection
func TestDSAPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "DSA private key header",
			input:     "-----BEGIN DSA PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "DSA public key (should not match)",
			input:     "-----BEGIN DSA PUBLIC KEY-----",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	dsaPattern := findPatternByName(patterns, "dsa_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if dsaPattern == nil {
				t.Skip("DSA private key pattern not implemented yet")
				return
			}
			matches := dsaPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test OpenSSH Private Key detection
func TestOpenSSHPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "OpenSSH private key header",
			input:     "-----BEGIN OPENSSH PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name: "full OpenSSH key",
			input: `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAAB...
-----END OPENSSH PRIVATE KEY-----`,
			wantMatch: true,
		},
		{
			name:      "SSH public key (should not match)",
			input:     "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	opensshPattern := findPatternByName(patterns, "openssh_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if opensshPattern == nil {
				t.Skip("OpenSSH private key pattern not implemented yet")
				return
			}
			matches := opensshPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test PGP Private Key detection
func TestPGPPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "PGP private key header",
			input:     "-----BEGIN PGP PRIVATE KEY BLOCK-----",
			wantMatch: true,
		},
		{
			name: "full PGP private key",
			input: `-----BEGIN PGP PRIVATE KEY BLOCK-----
Version: GnuPG v2

lQOYBF0...
-----END PGP PRIVATE KEY BLOCK-----`,
			wantMatch: true,
		},
		{
			name:      "PGP public key (should not match)",
			input:     "-----BEGIN PGP PUBLIC KEY BLOCK-----",
			wantMatch: false,
		},
		{
			name:      "PGP message (should not match)",
			input:     "-----BEGIN PGP MESSAGE-----",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	pgpPattern := findPatternByName(patterns, "pgp_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pgpPattern == nil {
				t.Skip("PGP private key pattern not implemented yet")
				return
			}
			matches := pgpPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test PKCS8 Private Key detection
func TestPKCS8PrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "PKCS8 private key header",
			input:     "-----BEGIN PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "encrypted PKCS8 private key",
			input:     "-----BEGIN ENCRYPTED PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name: "full PKCS8 key",
			input: `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQ...
-----END PRIVATE KEY-----`,
			wantMatch: true,
		},
		{
			name:      "public key (should not match)",
			input:     "-----BEGIN PUBLIC KEY-----",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	pkcs8Pattern := findPatternByName(patterns, "pkcs8_private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pkcs8Pattern == nil {
				t.Skip("PKCS8 private key pattern not implemented yet")
				return
			}
			matches := pkcs8Pattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test generic private key detection (catches all types)
func TestGenericPrivateKeyPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "RSA private key",
			input:     "-----BEGIN RSA PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "EC private key",
			input:     "-----BEGIN EC PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "DSA private key",
			input:     "-----BEGIN DSA PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "OpenSSH private key",
			input:     "-----BEGIN OPENSSH PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "PGP private key",
			input:     "-----BEGIN PGP PRIVATE KEY BLOCK-----",
			wantMatch: true,
		},
		{
			name:      "PKCS8 private key",
			input:     "-----BEGIN PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "encrypted PKCS8 private key",
			input:     "-----BEGIN ENCRYPTED PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "public key (should not match)",
			input:     "-----BEGIN PUBLIC KEY-----",
			wantMatch: false,
		},
		{
			name:      "certificate (should not match)",
			input:     "-----BEGIN CERTIFICATE-----",
			wantMatch: false,
		},
	}

	patterns := GetKeyPatterns()
	genericPattern := findPatternByName(patterns, "private_key")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if genericPattern == nil {
				t.Skip("Generic private key pattern not implemented yet")
				return
			}
			matches := genericPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}
