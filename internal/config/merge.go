// Package config provides configuration types and merge utilities for MCPProxy.
// This file implements smart config patching with deep merge semantics.
// Related issues: #239, #240
package config

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

// Error types for merge operations
var (
	// ErrImmutableField is returned when attempting to modify an immutable field
	ErrImmutableField = errors.New("cannot modify immutable field")

	// ErrInvalidConfig is returned when merged config fails validation
	ErrInvalidConfig = errors.New("invalid configuration after merge")
)

// ImmutableFieldError provides details about which immutable field was modified
type ImmutableFieldError struct {
	Field string
}

func (e *ImmutableFieldError) Error() string {
	return fmt.Sprintf("%s: %s", ErrImmutableField.Error(), e.Field)
}

func (e *ImmutableFieldError) Unwrap() error {
	return ErrImmutableField
}

// MergeOptions controls merge behavior
type MergeOptions struct {
	// GenerateDiff controls whether to compute changes for audit trail
	GenerateDiff bool

	// NullRemovesField controls whether nil pointer in patch removes the field
	// Default: true (RFC 7396 semantics)
	NullRemovesField bool

	// ImmutableFields lists fields that cannot be changed via merge
	// Default: ["name", "created"]
	ImmutableFields []string

	// removeMarkers tracks which fields should be removed (internal use)
	// This is populated when parsing JSON with explicit null values
	removeMarkers map[string]bool
}

// DefaultMergeOptions returns standard merge options
func DefaultMergeOptions() MergeOptions {
	return MergeOptions{
		GenerateDiff:     true,
		NullRemovesField: true,
		ImmutableFields:  []string{"name", "created"},
		removeMarkers:    make(map[string]bool),
	}
}

// WithRemoveMarker adds a field to the remove markers (used for explicit null handling)
func (o MergeOptions) WithRemoveMarker(field string) MergeOptions {
	if o.removeMarkers == nil {
		o.removeMarkers = make(map[string]bool)
	}
	o.removeMarkers[field] = true
	return o
}

// ShouldRemove checks if a field should be removed
func (o MergeOptions) ShouldRemove(field string) bool {
	if o.removeMarkers == nil {
		return false
	}
	return o.removeMarkers[field]
}

// ConfigDiff captures changes made during a merge operation for auditing
type ConfigDiff struct {
	// Modified fields with before/after values
	Modified map[string]FieldChange `json:"modified,omitempty"`

	// Fields that were added (didn't exist in base)
	Added []string `json:"added,omitempty"`

	// Fields that were removed (via null in patch)
	Removed []string `json:"removed,omitempty"`

	// Timestamp of the merge operation
	Timestamp time.Time `json:"timestamp"`
}

// FieldChange represents a single field modification
type FieldChange struct {
	// Path to the field (e.g., "isolation.image")
	Path string `json:"path"`

	// Value before merge
	From interface{} `json:"from"`

	// Value after merge
	To interface{} `json:"to"`
}

// NewConfigDiff creates a new ConfigDiff
func NewConfigDiff() *ConfigDiff {
	return &ConfigDiff{
		Modified:  make(map[string]FieldChange),
		Added:     []string{},
		Removed:   []string{},
		Timestamp: time.Now(),
	}
}

// IsEmpty returns true if no changes were made
func (d *ConfigDiff) IsEmpty() bool {
	return d == nil || (len(d.Modified) == 0 && len(d.Added) == 0 && len(d.Removed) == 0)
}

// MergeServerConfig deep merges patch into base, returning the merged config and diff.
//
// Merge semantics:
//   - Scalar fields: Replace if patch value is non-zero
//   - Map fields (env, headers): Deep merge, null values remove keys
//   - Struct fields (isolation, oauth): Deep merge nested fields
//   - Array fields (args, extra_args, scopes): Replace entirely
//   - Nil/omitted fields in patch: Preserve base value
//
// Returns:
//   - merged: The resulting merged configuration (new copy)
//   - diff: Changes made during merge (nil if opts.GenerateDiff is false)
//   - error: Validation or merge error
func MergeServerConfig(base, patch *ServerConfig, opts MergeOptions) (*ServerConfig, *ConfigDiff, error) {
	if base == nil {
		// If no base, patch becomes the new config
		if patch == nil {
			return nil, nil, fmt.Errorf("%w: both base and patch are nil", ErrInvalidConfig)
		}
		// Return a copy of patch
		merged := copyServerConfig(patch)
		merged.Updated = time.Now()
		return merged, NewConfigDiff(), nil
	}

	if patch == nil {
		// If no patch, return a copy of base
		merged := copyServerConfig(base)
		return merged, nil, nil
	}

	// Check immutable fields
	for _, field := range opts.ImmutableFields {
		switch field {
		case "name":
			if patch.Name != "" && patch.Name != base.Name {
				return nil, nil, &ImmutableFieldError{Field: "name"}
			}
		case "created":
			if !patch.Created.IsZero() && !patch.Created.Equal(base.Created) {
				return nil, nil, &ImmutableFieldError{Field: "created"}
			}
		}
	}

	// Start with a copy of base
	merged := copyServerConfig(base)

	// Track changes if requested
	var diff *ConfigDiff
	if opts.GenerateDiff {
		diff = NewConfigDiff()
	}

	// Merge scalar fields (non-zero values in patch override base)
	if patch.URL != "" && patch.URL != base.URL {
		if diff != nil {
			diff.Modified["url"] = FieldChange{Path: "url", From: base.URL, To: patch.URL}
		}
		merged.URL = patch.URL
	}

	if patch.Protocol != "" && patch.Protocol != base.Protocol {
		if diff != nil {
			diff.Modified["protocol"] = FieldChange{Path: "protocol", From: base.Protocol, To: patch.Protocol}
		}
		merged.Protocol = patch.Protocol
	}

	if patch.Command != "" && patch.Command != base.Command {
		if diff != nil {
			diff.Modified["command"] = FieldChange{Path: "command", From: base.Command, To: patch.Command}
		}
		merged.Command = patch.Command
	}

	if patch.WorkingDir != "" && patch.WorkingDir != base.WorkingDir {
		if diff != nil {
			diff.Modified["working_dir"] = FieldChange{Path: "working_dir", From: base.WorkingDir, To: patch.WorkingDir}
		}
		merged.WorkingDir = patch.WorkingDir
	}

	// Boolean fields - use reflection to detect if explicitly set
	// For booleans, we check if the patch has them set differently from base
	// Since Go booleans default to false, we need a different approach for explicit false
	// For now, we always apply the patch value if it differs from base
	if patch.Enabled != base.Enabled {
		if diff != nil {
			diff.Modified["enabled"] = FieldChange{Path: "enabled", From: base.Enabled, To: patch.Enabled}
		}
		merged.Enabled = patch.Enabled
	}

	if patch.Quarantined != base.Quarantined {
		if diff != nil {
			diff.Modified["quarantined"] = FieldChange{Path: "quarantined", From: base.Quarantined, To: patch.Quarantined}
		}
		merged.Quarantined = patch.Quarantined
	}

	// Array fields - replace entirely if provided in patch
	if patch.Args != nil {
		if diff != nil && !reflect.DeepEqual(base.Args, patch.Args) {
			diff.Modified["args"] = FieldChange{Path: "args", From: base.Args, To: patch.Args}
		}
		merged.Args = make([]string, len(patch.Args))
		copy(merged.Args, patch.Args)
	}

	// Map fields - deep merge
	if patch.Env != nil {
		if diff != nil && !reflect.DeepEqual(base.Env, patch.Env) {
			diff.Modified["env"] = FieldChange{Path: "env", From: base.Env, To: patch.Env}
		}
		merged.Env = MergeMap(base.Env, patch.Env)
	}

	if patch.Headers != nil {
		if diff != nil && !reflect.DeepEqual(base.Headers, patch.Headers) {
			diff.Modified["headers"] = FieldChange{Path: "headers", From: base.Headers, To: patch.Headers}
		}
		merged.Headers = MergeMap(base.Headers, patch.Headers)
	}

	// Nested struct fields - deep merge or remove
	// Handle Isolation
	if opts.ShouldRemove("isolation") {
		if base.Isolation != nil {
			if diff != nil {
				diff.Removed = append(diff.Removed, "isolation")
			}
			merged.Isolation = nil
		}
	} else if patch.Isolation != nil {
		// Deep merge isolation configs
		newIsolation := MergeIsolationConfig(base.Isolation, patch.Isolation, false)
		if diff != nil && !reflect.DeepEqual(base.Isolation, newIsolation) {
			diff.Modified["isolation"] = FieldChange{Path: "isolation", From: base.Isolation, To: newIsolation}
		}
		merged.Isolation = newIsolation
	}

	// Handle OAuth
	if opts.ShouldRemove("oauth") {
		if base.OAuth != nil {
			if diff != nil {
				diff.Removed = append(diff.Removed, "oauth")
			}
			merged.OAuth = nil
		}
	} else if patch.OAuth != nil {
		// Deep merge OAuth configs
		newOAuth := MergeOAuthConfig(base.OAuth, patch.OAuth, false)
		if diff != nil && !reflect.DeepEqual(base.OAuth, newOAuth) {
			diff.Modified["oauth"] = FieldChange{Path: "oauth", From: base.OAuth, To: newOAuth}
		}
		merged.OAuth = newOAuth
	}

	// Always update the Updated timestamp
	merged.Updated = time.Now()

	return merged, diff, nil
}

// MergeMap deep merges src into dst, returning a new map.
// Empty string values in src do NOT remove keys (use explicit removal mechanism).
// The original maps are not modified.
func MergeMap(dst, src map[string]string) map[string]string {
	if dst == nil && src == nil {
		return nil
	}

	result := make(map[string]string)

	// Copy dst first
	for k, v := range dst {
		result[k] = v
	}

	// Merge src - add/update keys
	for k, v := range src {
		result[k] = v
	}

	return result
}

// MergeMapWithRemoval deep merges src into dst with support for key removal.
// Keys with empty string values in src will be removed from the result.
func MergeMapWithRemoval(dst, src map[string]string) map[string]string {
	if dst == nil && src == nil {
		return nil
	}

	result := make(map[string]string)

	// Copy dst first
	for k, v := range dst {
		result[k] = v
	}

	// Merge src - add/update/remove keys
	for k, v := range src {
		if v == "" {
			// Empty string signals removal
			delete(result, k)
		} else {
			result[k] = v
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// MergeIsolationConfig deep merges patch into base isolation config.
// Returns nil if both are nil, or if removeIfNil is true and patch is nil.
func MergeIsolationConfig(base, patch *IsolationConfig, removeIfNil bool) *IsolationConfig {
	if base == nil && patch == nil {
		return nil
	}

	if patch == nil {
		if removeIfNil {
			return nil
		}
		// Return a copy of base
		return copyIsolationConfig(base)
	}

	if base == nil {
		// Return a copy of patch
		return copyIsolationConfig(patch)
	}

	// Deep merge
	result := copyIsolationConfig(base)

	// Merge scalar fields - non-zero values override
	// For Enabled, we always take patch value since it's a boolean
	result.Enabled = patch.Enabled

	if patch.Image != "" {
		result.Image = patch.Image
	}
	if patch.NetworkMode != "" {
		result.NetworkMode = patch.NetworkMode
	}
	if patch.WorkingDir != "" {
		result.WorkingDir = patch.WorkingDir
	}
	if patch.LogDriver != "" {
		result.LogDriver = patch.LogDriver
	}
	if patch.LogMaxSize != "" {
		result.LogMaxSize = patch.LogMaxSize
	}
	if patch.LogMaxFiles != "" {
		result.LogMaxFiles = patch.LogMaxFiles
	}

	// Array fields - replace entirely if provided
	if patch.ExtraArgs != nil {
		result.ExtraArgs = make([]string, len(patch.ExtraArgs))
		copy(result.ExtraArgs, patch.ExtraArgs)
	}

	return result
}

// MergeOAuthConfig deep merges patch into base OAuth config.
// Returns nil if both are nil, or if removeIfNil is true and patch is nil.
func MergeOAuthConfig(base, patch *OAuthConfig, removeIfNil bool) *OAuthConfig {
	if base == nil && patch == nil {
		return nil
	}

	if patch == nil {
		if removeIfNil {
			return nil
		}
		// Return a copy of base
		return copyOAuthConfig(base)
	}

	if base == nil {
		// Return a copy of patch
		return copyOAuthConfig(patch)
	}

	// Deep merge
	result := copyOAuthConfig(base)

	// Merge scalar fields - non-zero values override
	if patch.ClientID != "" {
		result.ClientID = patch.ClientID
	}
	if patch.ClientSecret != "" {
		result.ClientSecret = patch.ClientSecret
	}
	if patch.RedirectURI != "" {
		result.RedirectURI = patch.RedirectURI
	}

	// Boolean - always take patch value
	result.PKCEEnabled = patch.PKCEEnabled

	// Array fields - replace entirely if provided
	if patch.Scopes != nil {
		result.Scopes = make([]string, len(patch.Scopes))
		copy(result.Scopes, patch.Scopes)
	}

	// Map fields - deep merge
	if patch.ExtraParams != nil {
		result.ExtraParams = MergeMap(base.ExtraParams, patch.ExtraParams)
	}

	return result
}

// Helper functions to copy configs (avoiding pointer aliasing)

func copyServerConfig(src *ServerConfig) *ServerConfig {
	if src == nil {
		return nil
	}

	dst := &ServerConfig{
		Name:        src.Name,
		URL:         src.URL,
		Protocol:    src.Protocol,
		Command:     src.Command,
		WorkingDir:  src.WorkingDir,
		Enabled:     src.Enabled,
		Quarantined: src.Quarantined,
		Created:     src.Created,
		Updated:     src.Updated,
	}

	// Copy slices
	if src.Args != nil {
		dst.Args = make([]string, len(src.Args))
		copy(dst.Args, src.Args)
	}

	// Copy maps
	if src.Env != nil {
		dst.Env = make(map[string]string, len(src.Env))
		for k, v := range src.Env {
			dst.Env[k] = v
		}
	}
	if src.Headers != nil {
		dst.Headers = make(map[string]string, len(src.Headers))
		for k, v := range src.Headers {
			dst.Headers[k] = v
		}
	}

	// Copy nested structs
	dst.Isolation = copyIsolationConfig(src.Isolation)
	dst.OAuth = copyOAuthConfig(src.OAuth)

	return dst
}

func copyIsolationConfig(src *IsolationConfig) *IsolationConfig {
	if src == nil {
		return nil
	}

	dst := &IsolationConfig{
		Enabled:     src.Enabled,
		Image:       src.Image,
		NetworkMode: src.NetworkMode,
		WorkingDir:  src.WorkingDir,
		LogDriver:   src.LogDriver,
		LogMaxSize:  src.LogMaxSize,
		LogMaxFiles: src.LogMaxFiles,
	}

	if src.ExtraArgs != nil {
		dst.ExtraArgs = make([]string, len(src.ExtraArgs))
		copy(dst.ExtraArgs, src.ExtraArgs)
	}

	return dst
}

func copyOAuthConfig(src *OAuthConfig) *OAuthConfig {
	if src == nil {
		return nil
	}

	dst := &OAuthConfig{
		ClientID:     src.ClientID,
		ClientSecret: src.ClientSecret,
		RedirectURI:  src.RedirectURI,
		PKCEEnabled:  src.PKCEEnabled,
	}

	if src.Scopes != nil {
		dst.Scopes = make([]string, len(src.Scopes))
		copy(dst.Scopes, src.Scopes)
	}

	if src.ExtraParams != nil {
		dst.ExtraParams = make(map[string]string, len(src.ExtraParams))
		for k, v := range src.ExtraParams {
			dst.ExtraParams[k] = v
		}
	}

	return dst
}
