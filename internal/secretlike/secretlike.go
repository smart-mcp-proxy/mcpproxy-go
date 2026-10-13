// Package secretlike implements the one shared "does this field name look
// like a credential?" rule (Spec 109 research D13), used to default the
// Value/Secret toggle to Secret in Paste, Manual and Catalog-with-inputs
// surfaces (FR-065), and to compute CatalogResult.RequiredInputs[].secret_like
// when a registry omits or falsifies its own isSecret flag (FR-061).
package secretlike

import "regexp"

// pattern is the D13 name heuristic, shared verbatim across Go, the frontend
// port (frontend/src/utils/secretRef.ts) and the Swift port
// (MCPProxyTests/CatalogTests.swift secret toggle model) so all three surfaces
// default the same fields to Secret.
var pattern = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|[_-]key$|auth|credential|private[_-]?key)`)

// LooksSecret reports whether name looks like it holds a credential.
func LooksSecret(name string) bool {
	return pattern.MatchString(name)
}
