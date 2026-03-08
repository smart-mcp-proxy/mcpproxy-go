# Data Model: MCPProxy Repo Restructure

This restructure introduces no new data entities. It adds build-time metadata only.

## Edition Metadata

| Field | Type | Source | Description |
|-------|------|--------|-------------|
| `Edition` | `string` | Build tag | `"personal"` (default) or `"teams"` (with `-tags teams`) |
| `Version` | `string` | ldflags | Semantic version from git tag |
| `Commit` | `string` | ldflags | Short git commit hash |
| `BuildDate` | `string` | ldflags | ISO 8601 UTC build timestamp |

## Status API Extension

The `/api/v1/status` response gains one field:

```json
{
  "version": "0.21.0",
  "edition": "personal",
  "... existing fields ..."
}
```

## Teams Feature Registry

The teams edition uses a registration pattern for feature modules:

```go
// internal/teams/registry.go
type Feature struct {
    Name    string
    Setup   func(deps Dependencies) error
}

var features []Feature

func Register(f Feature) {
    features = append(features, f)
}

func SetupAll(deps Dependencies) error {
    for _, f := range features {
        if err := f.Setup(deps); err != nil {
            return fmt.Errorf("teams feature %s: %w", f.Name, err)
        }
    }
    return nil
}
```

Future teams packages (auth, workspace, users) will call `teams.Register()` in their `init()` functions. This restructure creates the registry; no features register yet.
