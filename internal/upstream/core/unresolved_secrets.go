package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// UnresolvedSecretError reports secret references in the fields an HTTP
// upstream receives (URL, headers, OAuth settings) that could not be resolved
// when the client was built. Release QA F9: such a reference used to be sent
// to the upstream verbatim (`Authorization: Bearer ${keyring:name}`) while
// health stayed green. The client now refuses to connect, sign in or refresh
// a token, so nothing reaches the upstream, and the error says which
// reference is missing, where, and how to add it.
//
// The error names references (`${keyring:name}`) and field paths only, never
// a field's value: a value may hold literal text or other resolved secrets.
type UnresolvedSecretError struct {
	Server string
	Refs   []UnresolvedSecretRef
}

// UnresolvedSecretRef is one reference that could not be resolved.
type UnresolvedSecretRef struct {
	Field     string // e.g. "Headers[Authorization]", "OAuth.ClientSecret"
	Reference string // e.g. "${keyring:github-token}"
	Type      string // e.g. "keyring"
	Name      string // e.g. "github-token"
}

func (e *UnresolvedSecretError) Error() string {
	if len(e.Refs) == 0 {
		return fmt.Sprintf("unresolved secret reference in server %q; nothing was sent to the upstream", e.Server)
	}
	var b strings.Builder
	for i, r := range e.Refs {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "unresolved secret reference %s in %s", r.Reference, r.Field)
	}
	fmt.Fprintf(&b, ": the secret is not available, so nothing was sent to the upstream. %s", e.fixHint())
	return b.String()
}

// fixHint says how to make the first missing reference available and pick it
// up: secrets are resolved when the server's connection is created.
func (e *UnresolvedSecretError) fixHint() string {
	r := e.Refs[0]
	restart := fmt.Sprintf("then restart the server (mcpproxy upstream restart %s)", e.Server)
	switch r.Type {
	case "keyring":
		return fmt.Sprintf("Add it with: mcpproxy secrets set %s, %s", r.Name, restart)
	case "env":
		// The env provider reads mcpproxy's own environment, which a server
		// restart cannot change.
		return fmt.Sprintf("Set the environment variable %s and restart mcpproxy", r.Name)
	case "file":
		return fmt.Sprintf("Make the file %s readable by mcpproxy, %s", r.Name, restart)
	default:
		return "Make the secret available, " + restart
	}
}

// Unwrap lets diagnostics classify the failure as MCPX_CONFIG_MISSING_SECRET.
func (e *UnresolvedSecretError) Unwrap() error {
	return diagnostics.WrapConfigMissingSecret(errMissingSecret)
}

var errMissingSecret = errors.New("missing secret")

// IsUnresolvedSecretError reports whether err carries an UnresolvedSecretError.
func IsUnresolvedSecretError(err error) bool {
	var u *UnresolvedSecretError
	return errors.As(err, &u)
}

// wireSecretField reports whether a ServerConfig field path is sent to an HTTP
// upstream or its authorization server: the URL, the headers and every OAuth
// setting. It applies to HTTP-family servers only (see NewClientWithOptions);
// fields only a local process sees (command, args, env, working dir,
// isolation), and every field of a stdio server, keep the existing behaviour:
// logged, used as written.
func wireSecretField(path string) bool {
	return path == "URL" ||
		strings.HasPrefix(path, "Headers[") ||
		path == "OAuth" || strings.HasPrefix(path, "OAuth.")
}

// unresolvedWireSecrets builds the refusal for the expansion failures that
// affect fields sent over HTTP, or nil when there are none. A field whose
// expansion failed keeps its raw value, so its references are re-parsed and
// each one is checked; only the ones that fail are named.
func unresolvedWireSecrets(server string, errs []secret.SecretExpansionError, resolveOK func(secret.Ref) bool) *UnresolvedSecretError {
	out := &UnresolvedSecretError{Server: server}
	seen := map[string]bool{}
	for _, e := range errs {
		if !wireSecretField(e.FieldPath) {
			continue
		}
		refs := secret.FindSecretRefs(e.Reference)
		added := false
		for _, ref := range refs {
			if resolveOK != nil && resolveOK(*ref) {
				continue
			}
			key := e.FieldPath + "\x00" + ref.Original
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Refs = append(out.Refs, UnresolvedSecretRef{Field: e.FieldPath, Reference: ref.Original, Type: ref.Type, Name: ref.Name})
			added = true
		}
		if !added && len(refs) > 0 {
			// The field failed but each reference resolves on its own now (a
			// secret added meanwhile): still refuse, since the client holds the
			// unresolved value, naming the field's first reference.
			r := refs[0]
			out.Refs = append(out.Refs, UnresolvedSecretRef{Field: e.FieldPath, Reference: r.Original, Type: r.Type, Name: r.Name})
		}
	}
	if len(out.Refs) == 0 {
		return nil
	}
	sort.SliceStable(out.Refs, func(i, j int) bool { return out.Refs[i].Field < out.Refs[j].Field })
	return out
}
