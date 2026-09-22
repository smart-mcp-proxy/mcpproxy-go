package config

// EffectiveAnnotationsForTool resolves the effective MCP tool annotations for a
// tool by merging upstream hints with per-server operator overrides.
// Precedence per hint: exact tool name > wildcard "*" > upstream.
// A nil upstream is treated as empty; an empty result (no hints) returns nil
// to preserve the MCP spec nil-default semantics.
func EffectiveAnnotationsForTool(overrides map[string]*ToolAnnotations, toolName string, upstream *ToolAnnotations) *ToolAnnotations {
	wild := overrides["*"]
	exact := overrides[toolName]
	if wild == nil && exact == nil {
		return upstream
	}
	base := &ToolAnnotations{}
	if upstream != nil {
		*base = *upstream
		if upstream.ReadOnlyHint != nil {
			b := *upstream.ReadOnlyHint
			base.ReadOnlyHint = &b
		}
		if upstream.DestructiveHint != nil {
			b := *upstream.DestructiveHint
			base.DestructiveHint = &b
		}
		if upstream.IdempotentHint != nil {
			b := *upstream.IdempotentHint
			base.IdempotentHint = &b
		}
		if upstream.OpenWorldHint != nil {
			b := *upstream.OpenWorldHint
			base.OpenWorldHint = &b
		}
	}
	for _, ov := range []*ToolAnnotations{wild, exact} {
		if ov == nil {
			continue
		}
		if ov.Title != "" {
			base.Title = ov.Title
		}
		if ov.ReadOnlyHint != nil {
			b := *ov.ReadOnlyHint
			base.ReadOnlyHint = &b
		}
		if ov.DestructiveHint != nil {
			b := *ov.DestructiveHint
			base.DestructiveHint = &b
		}
		if ov.IdempotentHint != nil {
			b := *ov.IdempotentHint
			base.IdempotentHint = &b
		}
		if ov.OpenWorldHint != nil {
			b := *ov.OpenWorldHint
			base.OpenWorldHint = &b
		}
	}
	if base.Title == "" && base.ReadOnlyHint == nil && base.DestructiveHint == nil && base.IdempotentHint == nil && base.OpenWorldHint == nil {
		return nil
	}
	return base
}

// CloneAnnotationOverrides deep-copies an annotation_overrides map (values and
// their *bool hints) so hot-reload snapshots never alias the caller's map.
// Nil in, nil out; nil values are preserved as nil.
func CloneAnnotationOverrides(m map[string]*ToolAnnotations) map[string]*ToolAnnotations {
	if m == nil {
		return nil
	}
	out := make(map[string]*ToolAnnotations, len(m))
	for k, v := range m {
		if v == nil {
			out[k] = nil
			continue
		}
		cp := *v
		if v.ReadOnlyHint != nil {
			b := *v.ReadOnlyHint
			cp.ReadOnlyHint = &b
		}
		if v.DestructiveHint != nil {
			b := *v.DestructiveHint
			cp.DestructiveHint = &b
		}
		if v.IdempotentHint != nil {
			b := *v.IdempotentHint
			cp.IdempotentHint = &b
		}
		if v.OpenWorldHint != nil {
			b := *v.OpenWorldHint
			cp.OpenWorldHint = &b
		}
		out[k] = &cp
	}
	return out
}

// NormalizeAnnotationOverrides prunes empty entries where all hints are nil
// and Title is empty (legacy "* {}"), and enforces the 100-entry cap by
// truncating (validation will still reject >100 on write, but load-time
// convergence must not panic). No-op if cfg or Servers is nil.
func NormalizeAnnotationOverrides(cfg *Config) {
	if cfg == nil {
		return
	}
	for _, s := range cfg.Servers {
		if s == nil || s.AnnotationOverrides == nil {
			continue
		}
		for k, v := range s.AnnotationOverrides {
			if v == nil {
				delete(s.AnnotationOverrides, k)
				continue
			}
			if v.Title == "" && v.ReadOnlyHint == nil && v.DestructiveHint == nil && v.IdempotentHint == nil && v.OpenWorldHint == nil {
				delete(s.AnnotationOverrides, k)
			}
		}
		if len(s.AnnotationOverrides) == 0 {
			s.AnnotationOverrides = nil
		}
	}
}
