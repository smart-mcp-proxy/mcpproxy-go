package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// applySecretFlags stores each --secret-env/--secret-header value in the OS
// keyring under its Spec 109 FR-065 ref name (internal/secret.RefName) and
// merges ${keyring:<ref>} into env/headers in place, so the caller's
// AddServerRequest never carries the raw value. It refuses outright — before
// writing anything — when the keyring is unavailable, naming the provider's
// own reason, rather than silently falling back to plaintext.
func applySecretFlags(resolver *secret.Resolver, serverName string, secretEnvs, secretHeaders []string, env, headers map[string]string) error {
	if len(secretEnvs) == 0 && len(secretHeaders) == 0 {
		return nil
	}

	if ok, reason := resolver.KeyringAvailability(); !ok {
		return fmt.Errorf("cannot store --secret-env/--secret-header: OS keyring unavailable (%s)", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	existing, err := resolver.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to list existing keyring entries: %w", err)
	}
	taken := make(map[string]bool, len(existing))
	for _, ref := range existing {
		if ref.Type == secret.SecretTypeKeyring {
			taken[ref.Name] = true
		}
	}
	// A ref this call itself just wrote must also count as taken for the
	// remaining flags in the same invocation, or two identically-named
	// --secret-env flags (or a rerun before ListAll's cache would see it)
	// would compute the same ref twice instead of a[-2] suffix (D28: never
	// overwrite an existing secret).
	takenFn := func(n string) bool { return taken[n] }

	for _, kv := range secretEnvs {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid --secret-env format: %q (expected KEY=value)", kv)
		}
		ref := secret.RefName(serverName, "env", name, takenFn)
		if err := resolver.Store(ctx, secret.Ref{Type: secret.SecretTypeKeyring, Name: ref}, value); err != nil {
			return fmt.Errorf("failed to store secret for env %s: %w", name, err)
		}
		taken[ref] = true
		env[name] = fmt.Sprintf("${keyring:%s}", ref)
	}

	for _, kv := range secretHeaders {
		name, value, ok := strings.Cut(kv, ":")
		if !ok {
			return fmt.Errorf("invalid --secret-header format: %q (expected 'Name: value')", kv)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		ref := secret.RefName(serverName, "header", name, takenFn)
		if err := resolver.Store(ctx, secret.Ref{Type: secret.SecretTypeKeyring, Name: ref}, value); err != nil {
			return fmt.Errorf("failed to store secret for header %s: %w", name, err)
		}
		taken[ref] = true
		headers[name] = fmt.Sprintf("${keyring:%s}", ref)
	}

	return nil
}
