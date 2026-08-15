// Package toolannotations is the single source of truth for MCP tool-annotation
// filter semantics (Spec 035 F4, Spec 094 FR-004).
//
// It is a leaf package: it depends only on internal/config for the annotation
// shape and performs no I/O whatsoever. It was extracted from
// internal/server/mcp_annotations.go so that lower-level consumers — notably
// the Spec 098 preflight evaluator — can classify a tool against the same
// filters the retrieve_tools handler applies, without importing internal/server
// (which would be an import cycle) and without re-implementing the semantics
// (which would let the two drift).
//
// The behavior is byte-identical to the pre-extraction implementation; the
// server package now delegates to these functions.
package toolannotations

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// Filter parameter names, used as the diagnostics map keys (Spec 094 FR-003)
// and interpolated literally into the suggestion string (FR-006).
const (
	FilterKeyReadOnlyOnly     = "read_only_only"
	FilterKeyExcludeDestruct  = "exclude_destructive"
	FilterKeyExcludeOpenWorld = "exclude_open_world"
)

// Filters is the set of annotation filters a caller activated. The zero value
// means "no filtering", for which every tool passes.
type Filters struct {
	ReadOnlyOnly       bool
	ExcludeDestructive bool
	ExcludeOpenWorld   bool
}

// Any reports whether at least one filter is active.
func (f Filters) Any() bool {
	return f.ReadOnlyOnly || f.ExcludeDestructive || f.ExcludeOpenWorld
}

// ExcludeReason decides whether a tool is excluded and, when it is, which
// filter is responsible and why (Spec 094 FR-004). It is the single source of
// truth for the filter semantics — ShouldExclude delegates to it, so the
// diagnostics can never describe a different filter than the one that ran.
//
// Filters are evaluated read-only → destructive → open-world and the FIRST one
// that excludes the tool owns the omission, which keeps per-filter counts
// summable (no double counting). `explicit` distinguishes an omission caused by
// an explicitly unsafe hint (remediation: none, the filter is working) from one
// caused by absent/unset annotations (remediation: fix upstream metadata).
func ExcludeReason(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) (filterKey string, explicit, excluded bool) {
	if readOnlyOnly {
		// Must have explicit readOnlyHint=true to pass
		if annotations == nil || annotations.ReadOnlyHint == nil {
			return FilterKeyReadOnlyOnly, false, true
		}
		if !*annotations.ReadOnlyHint {
			return FilterKeyReadOnlyOnly, true, true
		}
	}

	if excludeDestructive {
		// Exclude if destructiveHint is true or nil (default is true per spec).
		// However, a tool with readOnlyHint=true is inherently non-destructive,
		// so treat destructiveHint as false when readOnlyHint is explicitly true.
		isReadOnly := annotations != nil && annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint
		if !isReadOnly {
			if annotations == nil || annotations.DestructiveHint == nil {
				return FilterKeyExcludeDestruct, false, true
			}
			if *annotations.DestructiveHint {
				return FilterKeyExcludeDestruct, true, true
			}
		}
	}

	if excludeOpenWorld {
		// Exclude if openWorldHint is true or nil (default is true per spec)
		if annotations == nil || annotations.OpenWorldHint == nil {
			return FilterKeyExcludeOpenWorld, false, true
		}
		if *annotations.OpenWorldHint {
			return FilterKeyExcludeOpenWorld, true, true
		}
	}

	return "", false, false
}

// ExcludeReasonFor is the Filters-typed form of ExcludeReason, for callers that
// already carry a Filters value.
func ExcludeReasonFor(annotations *config.ToolAnnotations, f Filters) (filterKey string, explicit, excluded bool) {
	return ExcludeReason(annotations, f.ReadOnlyOnly, f.ExcludeDestructive, f.ExcludeOpenWorld)
}

// ShouldExclude returns true if a tool should be excluded based on its
// annotations and the active filters.
func ShouldExclude(annotations *config.ToolAnnotations, readOnlyOnly, excludeDestructive, excludeOpenWorld bool) bool {
	_, _, excluded := ExcludeReason(annotations, readOnlyOnly, excludeDestructive, excludeOpenWorld)
	return excluded
}
