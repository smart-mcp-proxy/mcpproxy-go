# Merge API Contract

**Feature Branch**: `023-smart-config-patch`
**Created**: 2026-01-10

## Overview

This document defines the internal API contract for the config merge utility. This is not a REST API but an internal Go package API.

## Package: `internal/config`

### Function: MergeServerConfig

Merges a partial configuration (patch) into an existing configuration (base).

```go
// MergeServerConfig deep merges patch into base, returning the merged config and diff
//
// Merge semantics:
//   - Scalar fields: Replace if patch value is non-zero
//   - Map fields (env, headers): Deep merge, null values remove keys
//   - Struct fields (isolation, oauth): Deep merge nested fields
//   - Array fields (args, extra_args, scopes): Replace entirely
//   - Nil/omitted fields in patch: Preserve base value
//
// Returns:
//   - merged: The resulting merged configuration
//   - diff: Changes made during merge (nil if opts.GenerateDiff is false)
//   - error: Validation or merge error
func MergeServerConfig(base, patch *ServerConfig, opts MergeOptions) (*ServerConfig, *ConfigDiff, error)
```

**Parameters**:

| Name | Type | Required | Description |
|------|------|----------|-------------|
| base | *ServerConfig | Yes | Existing server configuration to merge into |
| patch | *ServerConfig | Yes | Partial configuration with fields to update |
| opts | MergeOptions | Yes | Merge behavior options |

**Returns**:

| Name | Type | Description |
|------|------|-------------|
| merged | *ServerConfig | New merged configuration (base is not modified) |
| diff | *ConfigDiff | Changes made, nil if GenerateDiff is false |
| error | error | Validation error or nil |

**Errors**:

| Error | Condition |
|-------|-----------|
| `ErrImmutableField` | Attempt to modify Name or Created |
| `ErrInvalidConfig` | Merged config fails validation |

---

### Function: MergeMap

Merges two string maps with null-removal semantics.

```go
// MergeMap merges src into dst, returning a new map
// Null values in src remove keys from the result
func MergeMap(dst, src map[string]string) map[string]string
```

**Behavior**:
```go
// Example
dst := map[string]string{"a": "1", "b": "2"}
src := map[string]string{"b": "3", "c": "4"}  // b is updated, c is added
result := MergeMap(dst, src)
// result = {"a": "1", "b": "3", "c": "4"}

// Null removal (via special marker)
src := map[string]string{"b": ""}  // Empty string does NOT remove
// To remove, use the RemoveMarker constant or set to nil in JSON
```

---

### Function: MergeIsolationConfig

Merges isolation configurations.

```go
// MergeIsolationConfig merges patch into base isolation config
// Returns nil if both are nil, or if patch explicitly sets to nil (removal)
func MergeIsolationConfig(base, patch *IsolationConfig, removeIfNil bool) *IsolationConfig
```

**Behavior**:

| base | patch | removeIfNil | Result |
|------|-------|-------------|--------|
| nil | nil | any | nil |
| nil | non-nil | any | copy of patch |
| non-nil | nil | false | copy of base |
| non-nil | nil | true | nil (removed) |
| non-nil | non-nil | any | merged |

---

### Function: MergeOAuthConfig

Merges OAuth configurations.

```go
// MergeOAuthConfig merges patch into base OAuth config
func MergeOAuthConfig(base, patch *OAuthConfig, removeIfNil bool) *OAuthConfig
```

Same behavior pattern as MergeIsolationConfig.

---

## Types

### MergeOptions

```go
type MergeOptions struct {
    // GenerateDiff controls whether to compute changes
    GenerateDiff bool

    // NullRemovesField controls whether nil pointer in patch removes the field
    // Default: true (RFC 7396 semantics)
    NullRemovesField bool

    // ImmutableFields lists fields that cannot be changed
    // Default: ["name", "created"]
    ImmutableFields []string
}

// DefaultMergeOptions returns standard options
func DefaultMergeOptions() MergeOptions
```

### ConfigDiff

```go
type ConfigDiff struct {
    Modified  map[string]FieldChange `json:"modified,omitempty"`
    Added     []string               `json:"added,omitempty"`
    Removed   []string               `json:"removed,omitempty"`
    Timestamp time.Time              `json:"timestamp"`
}

type FieldChange struct {
    Path string      `json:"path"`
    From interface{} `json:"from"`
    To   interface{} `json:"to"`
}
```

---

## Usage Examples

### Basic Patch (Enable Server)

```go
base := &config.ServerConfig{
    Name:    "github-server",
    Enabled: false,
    Isolation: &config.IsolationConfig{
        Enabled: true,
        Image:   "python:3.11",
    },
}

patch := &config.ServerConfig{
    Enabled: true,  // Only change this
}

merged, diff, err := config.MergeServerConfig(base, patch, config.DefaultMergeOptions())
// merged.Enabled = true
// merged.Isolation = {Enabled: true, Image: "python:3.11"}  // Preserved!
// diff.Modified = {"enabled": {From: false, To: true}}
```

### Deep Merge Nested Object

```go
base := &config.ServerConfig{
    Name: "server",
    Isolation: &config.IsolationConfig{
        Enabled:     true,
        Image:       "python:3.11",
        NetworkMode: "bridge",
    },
}

patch := &config.ServerConfig{
    Isolation: &config.IsolationConfig{
        Image: "python:3.12",  // Only update image
    },
}

merged, _, _ := config.MergeServerConfig(base, patch, config.DefaultMergeOptions())
// merged.Isolation = {
//     Enabled: true,           // Preserved
//     Image: "python:3.12",    // Updated
//     NetworkMode: "bridge",   // Preserved
// }
```

### Remove Nested Object

```go
base := &config.ServerConfig{
    Name: "server",
    Isolation: &config.IsolationConfig{Enabled: true},
}

patch := &config.ServerConfig{
    Isolation: nil,  // Explicit nil = remove
}

opts := config.DefaultMergeOptions()
opts.NullRemovesField = true

merged, diff, _ := config.MergeServerConfig(base, patch, opts)
// merged.Isolation = nil
// diff.Removed = ["isolation"]
```

### Merge Environment Variables

```go
base := &config.ServerConfig{
    Name: "server",
    Env: map[string]string{
        "API_KEY": "xxx",
        "DEBUG":   "false",
    },
}

patch := &config.ServerConfig{
    Env: map[string]string{
        "DEBUG":   "true",   // Update
        "TIMEOUT": "30",     // Add
    },
}

merged, _, _ := config.MergeServerConfig(base, patch, config.DefaultMergeOptions())
// merged.Env = {"API_KEY": "xxx", "DEBUG": "true", "TIMEOUT": "30"}
```

---

## Error Handling

```go
// Attempt to change immutable field
patch := &config.ServerConfig{Name: "new-name"}
_, _, err := config.MergeServerConfig(base, patch, opts)
// err = ErrImmutableField{"name"}

// Handle errors
if errors.Is(err, config.ErrImmutableField) {
    // Cannot modify name
}
```

---

## Thread Safety

The merge functions are **stateless and thread-safe**. They do not modify the input parameters (base is copied before merging).

```go
// Safe for concurrent use
go func() {
    merged1, _, _ := config.MergeServerConfig(base, patch1, opts)
}()
go func() {
    merged2, _, _ := config.MergeServerConfig(base, patch2, opts)
}()
```
