package auth

import "testing"

// TestConstantTimeEqual pins the contract of the shared credential comparison
// helper: a timing-safe equality check that treats an empty credential as a
// non-match.
//
// The empty case is the important one. crypto/subtle.ConstantTimeCompare
// returns 1 for two empty slices, so a naive swap of `token == cfg.APIKey`
// for subtle.ConstantTimeCompare would turn an absent credential into an
// admin match wherever the caller's `!= ""` guard was dropped. The helper
// therefore rejects empty inputs itself.
func TestConstantTimeEqual(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"equal non-empty", "my-admin-key", "my-admin-key", true},
		{"equal long hex", "0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef", true},
		{"differs same length", "my-admin-key", "my-admin-keY", false},
		{"differs in length", "my-admin-key", "my-admin-key-longer", false},
		{"empty candidate", "", "my-admin-key", false},
		{"empty secret", "my-admin-key", "", false},
		{"both empty must not match", "", "", false},
		{"prefix of secret", "my-admin", "my-admin-key", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConstantTimeEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("ConstantTimeEqual(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestConstantTimeEqual_Symmetric guards against an implementation that
// short-circuits on one argument only.
func TestConstantTimeEqual_Symmetric(t *testing.T) {
	pairs := [][2]string{
		{"a", "b"},
		{"", "b"},
		{"", ""},
		{"same", "same"},
		{"short", "longer-value"},
	}
	for _, p := range pairs {
		if ConstantTimeEqual(p[0], p[1]) != ConstantTimeEqual(p[1], p[0]) {
			t.Errorf("ConstantTimeEqual is not symmetric for (%q, %q)", p[0], p[1])
		}
	}
}
