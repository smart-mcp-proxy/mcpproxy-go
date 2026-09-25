package main

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// fakeKeyringProvider is an in-memory secret.Provider standing in for the OS
// keyring, so these tests never touch the real Keychain/Secret Service/WinCred.
type fakeKeyringProvider struct {
	available bool
	store     map[string]string
}

func newFakeKeyringProvider(available bool) *fakeKeyringProvider {
	return &fakeKeyringProvider{available: available, store: map[string]string{}}
}

func (f *fakeKeyringProvider) CanResolve(secretType string) bool { return secretType == "keyring" }
func (f *fakeKeyringProvider) Resolve(_ context.Context, ref secret.Ref) (string, error) {
	return f.store[ref.Name], nil
}
func (f *fakeKeyringProvider) Store(_ context.Context, ref secret.Ref, value string) error {
	f.store[ref.Name] = value
	return nil
}
func (f *fakeKeyringProvider) Delete(_ context.Context, ref secret.Ref) error {
	delete(f.store, ref.Name)
	return nil
}
func (f *fakeKeyringProvider) List(_ context.Context) ([]secret.Ref, error) {
	refs := make([]secret.Ref, 0, len(f.store))
	for name := range f.store {
		refs = append(refs, secret.Ref{Type: "keyring", Name: name})
	}
	return refs, nil
}
func (f *fakeKeyringProvider) IsAvailable() bool { return f.available }

func newTestSecretResolver(fake *fakeKeyringProvider) *secret.Resolver {
	r := secret.NewResolver()
	r.RegisterProvider("keyring", fake)
	return r
}

// TestApplySecretFlags_WritesKeyringRefsIntoEnvAndHeaders pins FR-065: values
// are written to the keyring, never left in env/headers, and both maps get
// ${keyring:<ref>} instead.
func TestApplySecretFlags_WritesKeyringRefsIntoEnvAndHeaders(t *testing.T) {
	fake := newFakeKeyringProvider(true)
	resolver := newTestSecretResolver(fake)

	env := map[string]string{}
	headers := map[string]string{}
	err := applySecretFlags(resolver, "github",
		[]string{"GITHUB_TOKEN=sk-live-abc123"},
		[]string{"Authorization: Bearer xyz"},
		env, headers,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envRef, ok := env["GITHUB_TOKEN"]
	if !ok || envRef != "${keyring:github-env-github-token}" {
		t.Errorf("unexpected env ref: %q", envRef)
	}
	if fake.store["github-env-github-token"] != "sk-live-abc123" {
		t.Errorf("expected the raw value stored in the keyring, got %q", fake.store["github-env-github-token"])
	}

	headerRef, ok := headers["Authorization"]
	if !ok || headerRef != "${keyring:github-header-authorization}" {
		t.Errorf("unexpected header ref: %q", headerRef)
	}
	if fake.store["github-header-authorization"] != "Bearer xyz" {
		t.Errorf("expected the raw header value stored in the keyring, got %q", fake.store["github-header-authorization"])
	}
}

// TestApplySecretFlags_EnvHeaderSameNameCollision pins FR-065's headline
// collision case: an env var and a header with the SAME NAME write two
// DISTINCT keyring entries, and each field resolves to its own ref.
func TestApplySecretFlags_EnvHeaderSameNameCollision(t *testing.T) {
	fake := newFakeKeyringProvider(true)
	resolver := newTestSecretResolver(fake)

	env := map[string]string{}
	headers := map[string]string{}
	err := applySecretFlags(resolver, "github",
		[]string{"API_KEY=env-value"},
		[]string{"API_KEY: header-value"},
		env, headers,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env["API_KEY"] != "${keyring:github-env-api-key}" {
		t.Errorf("unexpected env ref: %q", env["API_KEY"])
	}
	if headers["API_KEY"] != "${keyring:github-header-api-key}" {
		t.Errorf("unexpected header ref: %q", headers["API_KEY"])
	}
	if fake.store["github-env-api-key"] != "env-value" {
		t.Errorf("env-value stored under the wrong ref: %+v", fake.store)
	}
	if fake.store["github-header-api-key"] != "header-value" {
		t.Errorf("header-value stored under the wrong ref: %+v", fake.store)
	}
}

// TestApplySecretFlags_TakenNameGetsSuffix pins D28: a pre-existing keyring
// entry is left unchanged and the new ref gets a numeric suffix.
func TestApplySecretFlags_TakenNameGetsSuffix(t *testing.T) {
	fake := newFakeKeyringProvider(true)
	fake.store["github-env-api-key"] = "PRE-EXISTING-UNRELATED-VALUE"
	resolver := newTestSecretResolver(fake)

	env := map[string]string{}
	if err := applySecretFlags(resolver, "github", []string{"API_KEY=new-value"}, nil, env, map[string]string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env["API_KEY"] != "${keyring:github-env-api-key-2}" {
		t.Errorf("expected the -2 suffixed ref, got %q", env["API_KEY"])
	}
	if fake.store["github-env-api-key"] != "PRE-EXISTING-UNRELATED-VALUE" {
		t.Error("the pre-existing entry must be left unchanged (D28: never overwrite)")
	}
	if fake.store["github-env-api-key-2"] != "new-value" {
		t.Errorf("expected the new value under the -2 ref, got %+v", fake.store)
	}
}

// TestApplySecretFlags_KeyringUnavailableRefuses pins FR-065: an unavailable
// keyring refuses with the reason, writing nothing.
func TestApplySecretFlags_KeyringUnavailableRefuses(t *testing.T) {
	fake := newFakeKeyringProvider(false)
	resolver := newTestSecretResolver(fake)

	env := map[string]string{}
	err := applySecretFlags(resolver, "github", []string{"API_KEY=value"}, nil, env, map[string]string{})
	if err == nil {
		t.Fatal("expected an error when the keyring is unavailable")
	}
	if len(env) != 0 {
		t.Errorf("expected env untouched on refusal, got %+v", env)
	}
	if len(fake.store) != 0 {
		t.Errorf("expected nothing written to the keyring on refusal, got %+v", fake.store)
	}
}

// TestApplySecretFlags_NoFlagsIsNoOp pins that the OS keyring is never probed
// (and no error possible) when neither flag was passed.
func TestApplySecretFlags_NoFlagsIsNoOp(t *testing.T) {
	fake := newFakeKeyringProvider(false) // unavailable — must not matter
	resolver := newTestSecretResolver(fake)

	if err := applySecretFlags(resolver, "github", nil, nil, map[string]string{}, map[string]string{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestApplySecretFlags_InvalidFormat pins input validation for both flags.
func TestApplySecretFlags_InvalidFormat(t *testing.T) {
	fake := newFakeKeyringProvider(true)
	resolver := newTestSecretResolver(fake)

	if err := applySecretFlags(resolver, "github", []string{"NOEQUALS"}, nil, map[string]string{}, map[string]string{}); err == nil {
		t.Error("expected an error for a --secret-env value with no '='")
	}
	if err := applySecretFlags(resolver, "github", nil, []string{"NoColonHere"}, map[string]string{}, map[string]string{}); err == nil {
		t.Error("expected an error for a --secret-header value with no ':'")
	}
}
