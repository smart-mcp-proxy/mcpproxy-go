# MCPProxy Repo Restructure — Personal + Teams Foundation

**Date:** 2026-03-08
**Status:** Approved
**Author:** Algis Dumbris

---

## Decision Summary

MCPProxy personal and teams editions will be built from the **same repository** using Go build tags. No `pkg/` migration needed. No separate repos.

## Binary Architecture

- `go build ./cmd/mcpproxy` → **Personal edition** (default)
- `go build -tags teams ./cmd/mcpproxy` → **Teams edition**
- Teams-only code lives in `internal/teams/` with `//go:build teams` guards
- Teams features self-register via `init()` pattern
- Binary self-identifies edition in version output, startup logs, `/api/v1/status`

## Repository Structure

```
mcpproxy-go/
├── cmd/mcpproxy/
│   ├── main.go                  ← shared entry point
│   └── teams_register.go       ← //go:build teams
├── internal/
│   ├── teams/                   ← teams-only code (all build-tagged)
│   │   ├── auth/                ← OAuth authorization server
│   │   ├── providers/           ← Google, GitHub, Microsoft, OIDC
│   │   ├── workspace/           ← Per-user server resolution
│   │   ├── users/               ← User storage, credential vault
│   │   ├── templates/           ← Server template engine
│   │   └── middleware/          ← Teams auth middleware
│   └── ... (existing packages unchanged)
├── frontend/
│   └── src/
│       ├── views/teams/         ← Teams-only Vue pages
│       └── components/teams/    ← Teams-only components
├── native/
│   ├── macos/                   ← Swift tray app (Xcode project)
│   └── windows/                 ← C# tray app (VS solution)
├── Dockerfile                   ← Teams Docker image
└── .github/workflows/release.yml ← Extended for both editions
```

## Distribution Matrix

| Platform | Personal | Teams |
|----------|----------|-------|
| macOS | DMG (Swift tray + core) | Homebrew / binary tarball |
| Windows | MSI/EXE (C# tray + core) | N/A (server product) |
| Linux | tar.gz | Docker image, .deb, tar.gz |

## Release Model

Single GitHub release tag (e.g., `v0.21.0`) with all assets:
- Personal: DMG, EXE, tar.gz (6 platform combos)
- Teams: tar.gz, .deb (Linux amd64/arm64)
- Docker image pushed to `ghcr.io/smart-mcp-proxy/mcpproxy-teams`

## Frontend Strategy

Single Vue build for both editions. Teams pages are lazy-loaded routes. Backend returns 404 for teams routes in personal mode. No separate frontend builds needed.

## Native Tray Apps

Live in `native/` directory within the same repo. Swift for macOS, C# for Windows. Replace the current Go tray app (`cmd/mcpproxy-tray/`).

## Key Design Decisions

1. **Build tags over separate cmd/**: Zero main() duplication, clean plugin pattern
2. **No pkg/ migration**: internal/ stays internal, both editions in same module
3. **Single release**: One tag, one changelog, labeled assets
4. **Teams = server product**: No macOS/Windows installers, Docker is primary
5. **Frontend = one build**: Backend controls access, not build-time splitting
