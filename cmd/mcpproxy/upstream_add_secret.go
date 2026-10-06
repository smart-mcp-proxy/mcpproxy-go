package main

import (
	"context"
	"fmt"
	"os"
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
//
// It returns the refs THIS call wrote. On success the caller (runUpstreamAdd)
// is responsible for rolling them back if a LATER step (trust-mode
// validation happens first now, but config load, the daemon/config-mode add
// itself, or a --if-not-exists skip) fails or doesn't result in the server
// actually being added — this function has no visibility into those later
// steps. On its OWN failure (a bad flag format, or the second of two flags
// failing to store), it rolls back everything it wrote so far itself and
// returns no refs, so the caller never has to distinguish "nothing was
// written" from "something was written and already cleaned up".
func applySecretFlags(resolver *secret.Resolver, serverName string, secretEnvs, secretHeaders []string, env, headers map[string]string) ([]string, error) {
	if len(secretEnvs) == 0 && len(secretHeaders) == 0 {
		return nil, nil
	}

	if ok, reason := resolver.KeyringAvailability(); !ok {
		return nil, fmt.Errorf("cannot store --secret-env/--secret-header: OS keyring unavailable (%s)", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	existing, err := resolver.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing keyring entries: %w", err)
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

	var writtenRefs []string
	// fail rolls back everything written so far in this call before
	// returning err, so a failure partway through (e.g. the second of two
	// --secret-env flags) never leaves the first flag's secret orphaned.
	// A refused/failed delete (e.g. a backend that wedged after the earlier
	// Store) is reported in the returned error rather than dropped, so the
	// user learns which refs are still in the keyring.
	fail := func(err error) ([]string, error) {
		if orphaned := deleteKeyringRefs(ctx, resolver, writtenRefs); len(orphaned) > 0 {
			err = fmt.Errorf("%w; %s", err, orphanedRefsNote(orphaned))
		}
		return nil, err
	}

	for _, kv := range secretEnvs {
		name, value, ok := strings.Cut(kv, "=")
		name = strings.TrimSpace(name)
		if !ok {
			return fail(fmt.Errorf("invalid --secret-env format: %q (expected KEY=value)", kv))
		}
		if name == "" {
			return fail(fmt.Errorf("invalid --secret-env format: %q (empty name; expected KEY=value)", kv))
		}
		ref := secret.RefName(serverName, "env", name, takenFn)
		if err := resolver.Store(ctx, secret.Ref{Type: secret.SecretTypeKeyring, Name: ref}, value); err != nil {
			return fail(fmt.Errorf("failed to store secret for env %s: %w", name, err))
		}
		taken[ref] = true
		writtenRefs = append(writtenRefs, ref)
		env[name] = fmt.Sprintf("${keyring:%s}", ref)
	}

	for _, kv := range secretHeaders {
		name, value, ok := strings.Cut(kv, ":")
		if !ok {
			return fail(fmt.Errorf("invalid --secret-header format: %q (expected 'Name: value')", kv))
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			return fail(fmt.Errorf("invalid --secret-header format: %q (empty name; expected 'Name: value')", kv))
		}
		ref := secret.RefName(serverName, "header", name, takenFn)
		if err := resolver.Store(ctx, secret.Ref{Type: secret.SecretTypeKeyring, Name: ref}, value); err != nil {
			return fail(fmt.Errorf("failed to store secret for header %s: %w", name, err))
		}
		taken[ref] = true
		writtenRefs = append(writtenRefs, ref)
		headers[name] = fmt.Sprintf("${keyring:%s}", ref)
	}

	return writtenRefs, nil
}

// deleteKeyringRefs deletes each ref and returns those that could NOT be
// deleted. resolver.Delete refuses outright when the keyring provider reports
// itself unavailable, so on a wedged backend every ref comes back orphaned.
func deleteKeyringRefs(ctx context.Context, resolver *secret.Resolver, refs []string) []string {
	var orphaned []string
	for _, ref := range refs {
		if err := resolver.Delete(ctx, secret.Ref{Type: secret.SecretTypeKeyring, Name: ref}); err != nil {
			orphaned = append(orphaned, ref)
		}
	}
	return orphaned
}

// orphanedRefsNote is the user-facing warning naming refs a rollback could
// not remove, with the command that cleans them up once the keyring recovers.
func orphanedRefsNote(orphaned []string) string {
	return fmt.Sprintf("rollback incomplete: %s left in the keyring; remove with `mcpproxy secrets del <name>` once it is available",
		strings.Join(orphaned, ", "))
}

// rollbackKeyringRefs is the runUpstreamAdd-level counterpart of
// applySecretFlags' internal rollback: it deletes refs that WERE
// successfully written by applySecretFlags but must not survive because a
// later step (trust-mode validation, config load, the daemon/config-mode add
// itself, or a --if-not-exists skip) didn't result in the server actually
// being added. The original error is what the user needs to see, so a delete
// failure does not replace it, but refs left behind are warned about on
// stderr and returned.
func rollbackKeyringRefs(resolver *secret.Resolver, refs []string) []string {
	if len(refs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	orphaned := deleteKeyringRefs(ctx, resolver, refs)
	if len(orphaned) > 0 {
		fmt.Fprintln(os.Stderr, "Warning: "+orphanedRefsNote(orphaned))
	}
	return orphaned
}
