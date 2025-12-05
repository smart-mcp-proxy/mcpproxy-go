# Configuration Schema: OAuth Extra Parameters

**Feature**: OAuth Extra Parameters Support
**Date**: 2025-11-30

## JSON Schema

### OAuthConfig with ExtraParams

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "client_id": {
      "type": "string",
      "description": "OAuth 2.0 client identifier"
    },
    "client_secret": {
      "type": "string",
      "description": "OAuth 2.0 client secret (optional for PKCE flow)"
    },
    "redirect_uri": {
      "type": "string",
      "format": "uri",
      "description": "OAuth 2.0 redirect URI (auto-generated if omitted)"
    },
    "scopes": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "OAuth 2.0 scopes to request"
    },
    "pkce_enabled": {
      "type": "boolean",
      "default": true,
      "description": "Enable PKCE (Proof Key for Code Exchange)"
    },
    "extra_params": {
      "type": "object",
      "additionalProperties": {
        "type": "string"
      },
      "description": "Additional OAuth parameters (e.g., RFC 8707 resource)",
      "examples": [
        {
          "resource": "https://example.com/mcp",
          "audience": "mcp-api"
        }
      ]
    }
  },
  "required": ["client_id"],
  "additionalProperties": false
}
```

### Validation Rules

**Extra Params Constraints**:
```json
{
  "extra_params": {
    "type": "object",
    "not": {
      "anyOf": [
        { "required": ["client_id"] },
        { "required": ["client_secret"] },
        { "required": ["redirect_uri"] },
        { "required": ["response_type"] },
        { "required": ["scope"] },
        { "required": ["state"] },
        { "required": ["code_challenge"] },
        { "required": ["code_challenge_method"] },
        { "required": ["grant_type"] },
        { "required": ["code"] },
        { "required": ["refresh_token"] },
        { "required": ["code_verifier"] }
      ]
    },
    "errorMessage": "extra_params cannot override reserved OAuth 2.0 parameters"
  }
}
```

## Examples

### Minimal OAuth Config (Backward Compatible)

```json
{
  "oauth": {
    "client_id": "abc123"
  }
}
```

**Behavior**:
- `extra_params` is omitted → nil in Go
- OAuth flow uses standard parameters only

### RFC 8707 Resource Indicator

```json
{
  "oauth": {
    "client_id": "abc123",
    "client_secret": "secret",
    "scopes": ["read", "write"],
    "pkce_enabled": true,
    "extra_params": {
      "resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp"
    }
  }
}
```

**Behavior**:
- Authorization URL includes `&resource=https%3A%2F%2Foauth.runlayer.com...`
- Token requests include `resource` parameter in body

### Multiple Extra Parameters

```json
{
  "oauth": {
    "client_id": "abc123",
    "scopes": ["openid", "profile"],
    "extra_params": {
      "resource": "https://example.com/mcp",
      "audience": "mcp-api",
      "tenant": "tenant-123",
      "organization": "org-456"
    }
  }
}
```

**Behavior**:
- All extra params appended to OAuth URLs
- Order preserved (Go map iteration is randomized, but URL encoding is deterministic)

### Invalid Config (Reserved Parameter)

```json
{
  "oauth": {
    "client_id": "abc123",
    "extra_params": {
      "client_id": "malicious-override"
    }
  }
}
```

**Validation Error**:
```
oauth config validation failed: extra_params cannot override reserved OAuth 2.0 parameters: client_id
```

## Go Type Definition

```go
package config

// OAuthConfig represents OAuth configuration for a server
type OAuthConfig struct {
    ClientID     string            `json:"client_id,omitempty" mapstructure:"client_id"`
    ClientSecret string            `json:"client_secret,omitempty" mapstructure:"client_secret"`
    RedirectURI  string            `json:"redirect_uri,omitempty" mapstructure:"redirect_uri"`
    Scopes       []string          `json:"scopes,omitempty" mapstructure:"scopes"`
    PKCEEnabled  bool              `json:"pkce_enabled,omitempty" mapstructure:"pkce_enabled"`
    ExtraParams  map[string]string `json:"extra_params,omitempty" mapstructure:"extra_params"`
}

// Validate performs validation on OAuthConfig
func (o *OAuthConfig) Validate() error {
    if o == nil {
        return nil
    }
    return ValidateOAuthExtraParams(o.ExtraParams)
}
```

## mapstructure Tags

MCPProxy uses `mapstructure` for flexible configuration loading from JSON/YAML/TOML.

**Tag Mapping**:
- `client_id` → `ClientID`
- `client_secret` → `ClientSecret`
- `redirect_uri` → `RedirectURI`
- `scopes` → `Scopes`
- `pkce_enabled` → `PKCEEnabled`
- `extra_params` → `ExtraParams`

**Case Sensitivity**: mapstructure handles case-insensitive key matching automatically.

## References

- **Data Model**: [data-model.md](../data-model.md)
- **Implementation Plan**: [plan.md](../plan.md)
- **RFC 8707**: https://www.rfc-editor.org/rfc/rfc8707.html
