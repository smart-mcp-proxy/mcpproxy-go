# Data Model: RFC 8707 Resource Auto-Detection

**Feature**: 011-resource-auto-detect
**Date**: 2025-12-10

## Entities

### ProtectedResourceMetadata (Existing)

RFC 9728 Protected Resource Metadata document. Already exists in codebase but `Resource` field is currently discarded.

**Location**: `internal/oauth/discovery.go:15-21`

```go
type ProtectedResourceMetadata struct {
    Resource               string   `json:"resource"`               // RFC 8707 resource identifier
    ResourceName           string   `json:"resource_name,omitempty"`
    AuthorizationServers   []string `json:"authorization_servers"`
    BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
    ScopesSupported        []string `json:"scopes_supported,omitempty"`
}
```

**Fields**:
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| Resource | string | Yes | The resource identifier (URL) for RFC 8707 compliance |
| ResourceName | string | No | Human-readable name for the resource |
| AuthorizationServers | []string | Yes | OAuth authorization server URLs |
| BearerMethodsSupported | []string | No | Supported bearer token methods |
| ScopesSupported | []string | No | Available OAuth scopes |

### OAuthConfig (Existing)

Server OAuth configuration. Already has `ExtraParams` field for manual overrides.

**Location**: `internal/config/config.go:155-162`

```go
type OAuthConfig struct {
    ClientID     string            `json:"client_id,omitempty"`
    ClientSecret string            `json:"client_secret,omitempty"`
    RedirectURI  string            `json:"redirect_uri,omitempty"`
    Scopes       []string          `json:"scopes,omitempty"`
    PKCEEnabled  bool              `json:"pkce_enabled,omitempty"`
    ExtraParams  map[string]string `json:"extra_params,omitempty"` // Manual overrides
}
```

### ExtraParams (New Concept)

Runtime-computed OAuth extra parameters combining auto-detected and manual values.

**Not stored** - computed during OAuth flow from:
1. Auto-detected `resource` from Protected Resource Metadata
2. Fallback to server URL if metadata unavailable
3. Manual `extra_params` from config (overrides auto-detected)

```go
// Computed at runtime, not persisted
extraParams := map[string]string{
    "resource": resourceURL,  // Auto-detected or fallback
    // Plus any manual extra_params from config
}
```

## State Transitions

### OAuth Flow with Resource Detection

```
┌─────────────────┐
│  Start OAuth    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌──────────────────────┐
│ Create OAuth    │────►│ Discover PRM         │
│ Config          │     │ (extract resource)   │
└────────┬────────┘     └──────────┬───────────┘
         │                         │
         │    ┌────────────────────┘
         │    │
         ▼    ▼
┌─────────────────┐     ┌──────────────────────┐
│ Build           │     │ resource found?      │
│ extraParams     │◄────┤ Yes: use metadata    │
└────────┬────────┘     │ No: use server URL   │
         │              └──────────────────────┘
         │
         ▼
┌─────────────────┐
│ Merge manual    │
│ extra_params    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Get Auth URL    │
│ (from mcp-go)   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Inject extra    │
│ params into URL │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Launch browser  │
│ with modified   │
│ auth URL        │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Token exchange  │
│ (wrapper injects│
│ resource)       │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ OAuth Complete  │
└─────────────────┘
```

## Validation Rules

### Resource URL Validation

1. **Non-empty**: Resource URL must not be empty (fallback ensures this)
2. **Valid URL**: Must be parseable as a URL
3. **HTTPS preferred**: Should be HTTPS for security (warning if HTTP)

### ExtraParams Validation

Already implemented in `internal/config/oauth_validation.go`:

1. Reserved OAuth parameters cannot be overridden:
   - `client_id`, `client_secret`, `redirect_uri`
   - `response_type`, `scope`, `state`
   - `code_challenge`, `code_challenge_method`
   - `grant_type`, `code`, `refresh_token`, `code_verifier`

2. Validation occurs at config load time (fail-fast)

## Relationships

```
ServerConfig
    └── OAuthConfig (optional)
            └── ExtraParams (optional, manual overrides)

ProtectedResourceMetadata (runtime, from HTTP)
    └── Resource (auto-detected)

extraParams (runtime, computed)
    ├── resource (from PRM or fallback)
    └── manual extra_params (merged)
```

## No Database Schema Changes

This feature does not modify persistent storage. All changes are runtime-only:

1. **OAuth tokens** - Already stored in BBolt, no schema changes
2. **Server config** - `extra_params` field already exists
3. **Resource detection** - Computed at runtime, not persisted
