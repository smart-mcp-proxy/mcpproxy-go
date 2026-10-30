package secret

import (
	"context"
	"testing"

	"github.com/zalando/go-keyring"
)

// A secret removed outside mcpproxy (Keychain Access, `security
// delete-generic-password`) makes keyring.Delete return ErrNotFound. That must
// still clean the registry and must not mark the whole keyring unavailable.
func TestKeyringProvider_Delete_AlreadyGoneCleansRegistry(t *testing.T) {
	clearHeadlessEnv(t)
	registry := "foo\nbar"
	withKeyringMocks(t,
		func(_, user string) (string, error) {
			if user == RegistryKey {
				return registry, nil
			}
			return "", keyring.ErrNotFound
		},
		func(_, user, password string) error {
			if user == RegistryKey {
				registry = password
			}
			return nil
		},
		func(_, _ string) error { return keyring.ErrNotFound },
	)

	p := NewKeyringProvider()
	if err := p.Delete(context.Background(), Ref{Type: SecretTypeKeyring, Name: "foo"}); err != nil {
		t.Fatalf("Delete of an already-removed secret: %v", err)
	}
	if registry != "bar" {
		t.Errorf("registry = %q, want %q", registry, "bar")
	}
	if p.isKnownUnavailable() {
		t.Error("keyring marked unavailable after ErrNotFound on delete")
	}
	refs, err := p.List(context.Background())
	if err != nil || len(refs) != 1 || refs[0].Name != "bar" {
		t.Errorf("List = %v, %v; want only bar", refs, err)
	}
}
