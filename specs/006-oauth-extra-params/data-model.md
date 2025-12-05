# Data Model: OAuth Extra Parameters

**Feature**: OAuth Extra Parameters Support
**Date**: 2025-11-30

## Overview

This document defines the data structures and validation rules for OAuth extra parameters support. The design extends the existing `OAuthConfig` struct with an `ExtraParams` field while maintaining backward compatibility.

## Core Entities

### 1. OAuthConfig (Extended)

**Location**: `internal/config/config.go:154-162`

**Purpose**: Represents OAuth configuration for an MCP server, now including arbitrary extra parameters.

**Schema**:
```go
type OAuthConfig struct {
    ClientID     string            `json:"client_id,omitempty" mapstructure:"client_id"`
    ClientSecret string            `json:"client_secret,omitempty" mapstructure:"client_secret"`
    RedirectURI  string            `json:"redirect_uri,omitempty" mapstructure:"redirect_uri"`
    Scopes       []string          `json:"scopes,omitempty" mapstructure:"scopes"`
    PKCEEnabled  bool              `json:"pkce_enabled,omitempty" mapstructure:"pkce_enabled"`
    ExtraParams  map[string]string `json:"extra_params,omitempty" mapstructure:"extra_params"` // NEW
}
```

**Fields**:
- `ExtraParams`: Map of additional OAuth parameters (e.g., RFC 8707 `resource`)
  - **Type**: `map[string]string`
  - **Optional**: Yes (omitempty)
  - **Validation**: Keys must not be reserved OAuth 2.0 parameters
  - **Example**: `{"resource": "https://example.com/mcp", "audience": "mcp-api"}`

**Validation Rules**:
1. Keys in `ExtraParams` MUST NOT match reserved OAuth 2.0 parameters (see `ReservedOAuthParams`)
2. Validation is case-insensitive (e.g., `client_id`, `CLIENT_ID`, `Client_Id` all rejected)
3. Empty or nil `ExtraParams` is valid (backward compatibility)
4. Values can be any string (URL-encoding handled by wrapper)

**Lifecycle**:
1. Loaded from `mcp_config.json` via mapstructure
2. Validated at config load time (fail fast)
3. Passed to OAuth wrapper during client initialization
4. Persisted back to config file on updates

### 2. ReservedOAuthParams (New Constant)

**Location**: `internal/config/oauth_validation.go:6-17`

**Purpose**: Lookup table of OAuth 2.0 parameters that cannot be overridden via `ExtraParams`.

**Schema**:
```go
var reservedOAuthParams = map[string]bool{
    "client_id":             true,
    "client_secret":         true,
    "redirect_uri":          true,
    "response_type":         true,
    "scope":                 true,
    "state":                 true,
    "code_challenge":        true,
    "code_challenge_method": true,
    "grant_type":            true,
    "code":                  true,
    "refresh_token":         true,
    "code_verifier":         true,
}
```

**Rationale**:
- **Security**: Prevents malicious configs from hijacking OAuth flow
- **Correctness**: Ensures mcp-go's OAuth implementation controls critical parameters
- **Clarity**: Explicit list documents which params are reserved

**Maintenance**:
- Update when OAuth 2.0 spec adds new standard parameters
- Update when mcp-go adds support for new OAuth extensions

### 3. OAuthTransportWrapper (New)

**Location**: `internal/oauth/transport_wrapper.go` (to be created)

**Purpose**: Wraps mcp-go's OAuth handler to inject extra params into OAuth requests.

**Schema**:
```go
type OAuthTransportWrapper struct {
    inner       *client.OAuthHandler  // mcp-go OAuth handler
    extraParams map[string]string      // Extra params to inject
}

func NewOAuthTransportWrapper(handler *client.OAuthHandler, extraParams map[string]string) *OAuthTransportWrapper

func (w *OAuthTransportWrapper) WrapAuthorizationURL(baseURL string) string
func (w *OAuthTransportWrapper) WrapTokenRequest(req *http.Request) error
```

**Fields**:
- `inner`: The original mcp-go OAuth handler (delegation pattern)
- `extraParams`: Map of parameters to append to OAuth URLs

**Methods**:
- `WrapAuthorizationURL`: Appends extra params to authorization URL query string
- `WrapTokenRequest`: Adds extra params to token exchange/refresh request body

**State**: Stateless (pure function wrapper)

**Lifecycle**:
1. Created during OAuth client initialization
2. Used during OAuth flow (authorization, token exchange, refresh)
3. Discarded after OAuth flow completes

## Data Flow

### Configuration Loading

```
User Config File (mcp_config.json)
         │
         ├─► JSON Unmarshal
         │
         ├─► mapstructure Decode → config.OAuthConfig
         │
         ├─► Validate() → ValidateOAuthExtraParams()
         │   ├─► Check: ExtraParams keys not in reservedOAuthParams
         │   ├─► PASS: Continue
         │   └─► FAIL: Return error (config load fails)
         │
         └─► Stored in runtime.Config
```

### OAuth Flow with Extra Params

```
config.OAuthConfig.ExtraParams
         │
         ├─► CreateOAuthConfig() extracts
         │
         ├─► NewOAuthTransportWrapper(handler, extraParams)
         │
         ├─► OAuth Authorization Request
         │   ├─► mcp-go: GetAuthorizationURL() → base URL
         │   ├─► Wrapper: WrapAuthorizationURL(base) → modified URL
         │   └─► Browser: Opens modified URL (with &resource=...)
         │
         ├─► OAuth Token Exchange
         │   ├─► mcp-go: ProcessAuthorizationResponse() → token request
         │   ├─► Wrapper: WrapTokenRequest(req) → add extra params to body
         │   └─► Provider: Returns access token
         │
         └─► OAuth Token Refresh
             ├─► mcp-go: refreshToken() → refresh request
             ├─► Wrapper: WrapTokenRequest(req) → add extra params to body
             └─► Provider: Returns new access token
```

## Validation Rules

### Extra Params Validation

**Function**: `ValidateOAuthExtraParams(params map[string]string) error`
**Location**: `internal/config/oauth_validation.go:19-36`

**Rules**:
1. **Reserved Parameter Check** (MUST)
   - For each key in `params`:
     - Convert key to lowercase
     - If lowercase key exists in `reservedOAuthParams`:
       - Append key to `reservedKeys` list
   - If `reservedKeys` is non-empty:
     - Return error with list of reserved keys

2. **Empty/Nil Check** (SHOULD)
   - If `params` is nil or empty: return nil (no error)
   - Rationale: Backward compatibility with existing configs

**Error Format**:
```
extra_params cannot override reserved OAuth 2.0 parameters: client_id, state
```

**Test Coverage** (see `oauth_validation_test.go`):
- ✅ Nil params
- ✅ Empty params
- ✅ Valid RFC 8707 resource parameter
- ✅ Valid multiple custom parameters
- ✅ Invalid client_id override
- ✅ Invalid state override
- ✅ Invalid redirect_uri override
- ✅ Invalid multiple reserved params
- ✅ Valid + invalid mix
- ✅ Case-insensitive validation
- ✅ All reserved parameters (12 test cases)

## State Transitions

### OAuth Config Lifecycle

```
┌─────────────┐
│  Unloaded   │ (Config file not read yet)
└──────┬──────┘
       │ Load config from file
       ▼
┌─────────────┐
│   Loaded    │ (Parsed JSON, not validated)
└──────┬──────┘
       │ Validate() called
       ▼
    ┌──────────┐ NO
    │ Valid?   ├─────► ERROR (Config load fails)
    └────┬─────┘
         │ YES
         ▼
┌─────────────┐
│  Validated  │ (Ready for use in OAuth flow)
└──────┬──────┘
       │ Hot-reload triggered
       ▼
┌─────────────┐
│  Reloaded   │ (New config loaded, re-validated)
└─────────────┘
```

### Wrapper Lifecycle

```
┌───────────────┐
│ Not Created   │
└───────┬───────┘
        │ OAuth client initialization
        ▼
┌───────────────┐
│   Created     │ (Wrapper wraps mcp-go handler)
└───────┬───────┘
        │ Authorization request
        ▼
┌───────────────┐
│    Active     │ (Intercepting OAuth URLs)
└───────┬───────┘
        │ OAuth flow completes
        ▼
┌───────────────┐
│   Discarded   │ (Garbage collected)
└───────────────┘
```

## Relationships

### Entity Relationships

```
ServerConfig (1) ───────> (0..1) OAuthConfig
                                    │
                                    ├─► (0..1) ExtraParams map
                                    └─► Uses (1) ReservedOAuthParams (constant)

OAuthConfig ─────────────────────────> OAuthTransportWrapper
    (source)                              (created from)

OAuthTransportWrapper ───────────────> client.OAuthHandler
    (wraps)                              (mcp-go)
```

### Dependency Graph

```
config.OAuthConfig
    │
    ├─► config.ReservedOAuthParams (validation)
    │
    └─► oauth.OAuthTransportWrapper (runtime)
            │
            └─► client.OAuthHandler (mcp-go)
```

## Serialization Examples

### JSON Configuration

```json
{
  "mcpServers": [
    {
      "name": "runlayer-slack",
      "protocol": "streamable-http",
      "url": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
      "oauth": {
        "client_id": "abc123",
        "client_secret": "secret",
        "scopes": ["read", "write"],
        "pkce_enabled": true,
        "extra_params": {
          "resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
          "audience": "mcp-api",
          "tenant": "tenant-123"
        }
      }
    }
  ]
}
```

### Go Struct (In-Memory)

```go
oauthConfig := &config.OAuthConfig{
    ClientID:     "abc123",
    ClientSecret: "secret",
    Scopes:       []string{"read", "write"},
    PKCEEnabled:  true,
    ExtraParams: map[string]string{
        "resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
        "audience": "mcp-api",
        "tenant":   "tenant-123",
    },
}
```

### URL-Encoded OAuth Authorization URL

**Without extra_params**:
```
https://provider.com/authorize?
  response_type=code&
  client_id=abc123&
  redirect_uri=http://127.0.0.1:8080/callback&
  state=xyz789&
  scope=read+write&
  code_challenge=abc...&
  code_challenge_method=S256
```

**With extra_params**:
```
https://provider.com/authorize?
  response_type=code&
  client_id=abc123&
  redirect_uri=http://127.0.0.1:8080/callback&
  state=xyz789&
  scope=read+write&
  code_challenge=abc...&
  code_challenge_method=S256&
  resource=https%3A%2F%2Foauth.runlayer.com%2Fapi%2Fv1%2Fproxy%2FUUID%2Fmcp&
  audience=mcp-api&
  tenant=tenant-123
```

## Migration Path

### Backward Compatibility

**Existing Configs** (without `extra_params`):
```json
{
  "oauth": {
    "client_id": "abc123",
    "scopes": ["read"]
  }
}
```

**Behavior**:
- Loads successfully (omitempty field)
- `ExtraParams` is `nil`
- Wrapper skips injection (no-op)
- OAuth flow unchanged

**New Configs** (with `extra_params`):
```json
{
  "oauth": {
    "client_id": "abc123",
    "scopes": ["read"],
    "extra_params": {
      "resource": "https://example.com/mcp"
    }
  }
}
```

**Behavior**:
- Loads and validates extra params
- Wrapper injects params into OAuth URLs
- OAuth flow includes extra params

### Rollback Strategy

If extra_params causes issues:

1. **Remove from config**: Delete `extra_params` field → backward compatible
2. **Disable wrapper**: Set `extra_params: {}` → wrapper no-op
3. **Downgrade mcpproxy**: Older versions ignore unknown JSON fields → safe

No database migration required (OAuth tokens stored separately).

## References

- **Feature Spec**: [spec.md](spec.md)
- **Implementation Plan**: [plan.md](plan.md)
- **RFC 8707**: https://www.rfc-editor.org/rfc/rfc8707.html
- **mcp-go OAuthConfig**: https://github.com/mark3labs/mcp-go/blob/v0.43.1/client/oauth.go
