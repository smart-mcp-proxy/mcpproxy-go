# Data Model: Smart Config Patching

**Feature Branch**: `023-smart-config-patch`
**Created**: 2026-01-10

## Entities

### ServerConfig (Existing - Modified Behavior)

The existing `ServerConfig` struct remains unchanged. The modification is in how it's handled during updates.

```go
// internal/config/config.go
type ServerConfig struct {
    Name        string            `json:"name,omitempty"`
    URL         string            `json:"url,omitempty"`
    Protocol    string            `json:"protocol,omitempty"`
    Command     string            `json:"command,omitempty"`
    Args        []string          `json:"args,omitempty"`
    WorkingDir  string            `json:"working_dir,omitempty"`
    Env         map[string]string `json:"env,omitempty"`
    Headers     map[string]string `json:"headers,omitempty"`
    OAuth       *OAuthConfig      `json:"oauth,omitempty"`
    Enabled     bool              `json:"enabled"`
    Quarantined bool              `json:"quarantined"`
    Created     time.Time         `json:"created"`
    Updated     time.Time         `json:"updated,omitempty"`
    Isolation   *IsolationConfig  `json:"isolation,omitempty"`
}
```

**Merge Behavior per Field**:

| Field | Type | Merge Strategy | Notes |
|-------|------|----------------|-------|
| Name | string | Immutable | Cannot be changed via patch |
| URL | string | Replace | Overwrite if provided |
| Protocol | string | Replace | Overwrite if provided |
| Command | string | Replace | Overwrite if provided |
| Args | []string | **Replace Array** | Full array replacement |
| WorkingDir | string | Replace | Overwrite if provided |
| Env | map[string]string | **Deep Merge** | Merge keys, null removes |
| Headers | map[string]string | **Deep Merge** | Merge keys, null removes |
| OAuth | *OAuthConfig | **Deep Merge** | Merge nested fields |
| Enabled | bool | Replace | Overwrite if provided |
| Quarantined | bool | Replace | Overwrite if provided |
| Created | time.Time | Immutable | Never changed |
| Updated | time.Time | Auto-set | Always updated on modification |
| Isolation | *IsolationConfig | **Deep Merge** | Merge nested fields |

### IsolationConfig (Existing - Unchanged)

```go
// internal/config/config.go
type IsolationConfig struct {
    Enabled     bool     `json:"enabled"`
    Image       string   `json:"image,omitempty"`
    NetworkMode string   `json:"network_mode,omitempty"`
    ExtraArgs   []string `json:"extra_args,omitempty"`
    WorkingDir  string   `json:"working_dir,omitempty"`
    LogDriver   string   `json:"log_driver,omitempty"`
    LogMaxSize  string   `json:"log_max_size,omitempty"`
    LogMaxFiles string   `json:"log_max_files,omitempty"`
}
```

**Merge Behavior per Field**:

| Field | Type | Merge Strategy |
|-------|------|----------------|
| Enabled | bool | Replace |
| Image | string | Replace |
| NetworkMode | string | Replace |
| ExtraArgs | []string | **Replace Array** |
| WorkingDir | string | Replace |
| LogDriver | string | Replace |
| LogMaxSize | string | Replace |
| LogMaxFiles | string | Replace |

### OAuthConfig (Existing - Unchanged)

```go
// internal/config/config.go
type OAuthConfig struct {
    ClientID            string   `json:"client_id,omitempty"`
    ClientSecret        string   `json:"client_secret,omitempty"`
    AuthorizationURL    string   `json:"authorization_url,omitempty"`
    TokenURL            string   `json:"token_url,omitempty"`
    Scopes              []string `json:"scopes,omitempty"`
    CallbackPort        int      `json:"callback_port,omitempty"`
    ResourceURL         string   `json:"resource_url,omitempty"`
    ResourceAutodetect  bool     `json:"resource_autodetect,omitempty"`
}
```

**Merge Behavior per Field**:

| Field | Type | Merge Strategy |
|-------|------|----------------|
| ClientID | string | Replace |
| ClientSecret | string | Replace |
| AuthorizationURL | string | Replace |
| TokenURL | string | Replace |
| Scopes | []string | **Replace Array** |
| CallbackPort | int | Replace |
| ResourceURL | string | Replace |
| ResourceAutodetect | bool | Replace |

---

## New Entities

### ConfigDiff (New)

Captures the changes made during a config merge operation for auditing.

```go
// internal/config/merge.go
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

type FieldChange struct {
    // Path to the field (e.g., "isolation.image")
    Path string `json:"path"`

    // Value before merge (JSON-serialized)
    From interface{} `json:"from"`

    // Value after merge (JSON-serialized)
    To interface{} `json:"to"`
}
```

**Usage**:
```go
merged, diff, err := MergeServerConfig(base, patch)
if diff != nil {
    logger.Info("Config updated",
        zap.String("server", merged.Name),
        zap.Any("changes", diff.Modified))
}
```

### MergeOptions (New)

Configuration for merge behavior.

```go
// internal/config/merge.go
type MergeOptions struct {
    // Whether to generate a diff (for auditing)
    GenerateDiff bool

    // Whether null values in patch should remove fields
    // Default: true (RFC 7396 behavior)
    NullRemovesField bool

    // Fields that cannot be modified via patch
    ImmutableFields []string
}

// DefaultMergeOptions returns standard merge options
func DefaultMergeOptions() MergeOptions {
    return MergeOptions{
        GenerateDiff:     true,
        NullRemovesField: true,
        ImmutableFields:  []string{"name", "created"},
    }
}
```

---

## Entity Relationships

```
┌─────────────────────────────────────────────────────────────┐
│                      ServerConfig                            │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ Core Fields (Replace on patch)                          │ │
│  │   name, url, protocol, command, working_dir             │ │
│  │   enabled, quarantined                                  │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ Array Fields (Replace entirely on patch)                │ │
│  │   args []string                                         │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │ Map Fields (Deep merge on patch)                        │ │
│  │   env map[string]string                                 │ │
│  │   headers map[string]string                             │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                              │
│  ┌─────────────────────┐    ┌────────────────────────────┐  │
│  │   *OAuthConfig      │    │     *IsolationConfig       │  │
│  │   (Deep merge)      │    │     (Deep merge)           │  │
│  │                     │    │                            │  │
│  │ - ClientID          │    │ - Enabled                  │  │
│  │ - ClientSecret      │    │ - Image                    │  │
│  │ - AuthorizationURL  │    │ - NetworkMode              │  │
│  │ - TokenURL          │    │ - ExtraArgs [] (replace)   │  │
│  │ - Scopes [] (repl.) │    │ - WorkingDir               │  │
│  │ - CallbackPort      │    │ - LogDriver                │  │
│  │ - ResourceURL       │    │ - LogMaxSize               │  │
│  │ - ResourceAutodetect│    │ - LogMaxFiles              │  │
│  └─────────────────────┘    └────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## State Transitions

### Server Configuration Update Flow

```
┌──────────────────┐
│  Base Config     │
│  (from storage)  │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐     ┌──────────────────┐
│  Patch Request   │────▶│   MergeServer    │
│  (partial data)  │     │   Config()       │
└──────────────────┘     └────────┬─────────┘
                                  │
                    ┌─────────────┼─────────────┐
                    │             │             │
                    ▼             ▼             ▼
              ┌─────────┐  ┌───────────┐  ┌─────────┐
              │ Merged  │  │ ConfigDiff│  │  Error  │
              │ Config  │  │ (audit)   │  │(invalid)│
              └────┬────┘  └───────────┘  └─────────┘
                   │
                   ▼
         ┌──────────────────┐
         │ Validate Config  │
         └────────┬─────────┘
                  │
         ┌────────┴────────┐
         │                 │
         ▼                 ▼
   ┌──────────┐      ┌──────────┐
   │  Valid   │      │ Invalid  │
   │  Save    │      │  Reject  │
   └──────────┘      └──────────┘
```

---

## Validation Rules

### ServerConfig Validation (Existing + Enhanced)

| Field | Rule | Error Message |
|-------|------|---------------|
| Name | Required, immutable | "Server name is required" |
| Protocol | Must be valid enum | "Invalid protocol: must be one of stdio, http, sse, streamable-http, auto" |
| Command | Required if stdio | "Command is required for stdio servers" |
| URL | Required if http | "URL is required for HTTP servers" |

### Merge Validation

| Rule | Error Message |
|------|---------------|
| Name cannot be changed | "Cannot change server name via patch" |
| Created cannot be changed | "Cannot modify created timestamp" |
| Invalid nested JSON | "Invalid {field}_json format: {parse_error}" |

---

## Storage Mapping

### UpstreamRecord (BBolt Storage)

```go
// internal/storage/models.go
type UpstreamRecord struct {
    ID          string                  `json:"id"`
    Name        string                  `json:"name"`
    URL         string                  `json:"url,omitempty"`
    Protocol    string                  `json:"protocol,omitempty"`
    Command     string                  `json:"command,omitempty"`
    Args        []string                `json:"args,omitempty"`
    WorkingDir  string                  `json:"working_dir,omitempty"`
    Env         map[string]string       `json:"env,omitempty"`
    Headers     map[string]string       `json:"headers,omitempty"`
    OAuth       *config.OAuthConfig     `json:"oauth,omitempty"`
    Enabled     bool                    `json:"enabled"`
    Quarantined bool                    `json:"quarantined"`
    Created     time.Time               `json:"created"`
    Updated     time.Time               `json:"updated"`
    Isolation   *config.IsolationConfig `json:"isolation,omitempty"`  // MUST be included
}
```

**Critical**: All fields in `ServerConfig` MUST be present in `UpstreamRecord` to prevent data loss during storage operations.
