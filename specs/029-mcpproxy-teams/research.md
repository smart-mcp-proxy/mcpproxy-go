# Research: MCPProxy Repo Restructure

## Go Build Tags Pattern

**Decision**: Use `//go:build teams` file-level tags to isolate teams-only code.

**Rationale**: Go build tags are the idiomatic way to compile different feature sets from the same codebase. The `init()` registration pattern allows teams packages to self-register without modifying shared code paths.

**Alternatives considered**:
- Separate `cmd/` entries: Rejected — duplicates main() boilerplate, harder to maintain
- `pkg/` extraction: Rejected — unnecessary complexity when both editions share the same module
- Runtime feature flags: Rejected — dead code in personal binary, larger binary size

## Edition Identification

**Decision**: Use build-time `ldflags` variable + build-tagged source file for edition detection.

**Rationale**: Two-pronged approach:
1. `edition.go` (default) sets `Edition = "personal"`
2. `edition_teams.go` (build-tagged) overrides `Edition = "teams"`

This ensures the binary always knows its edition without runtime config. The edition is exposed in:
- `mcpproxy version` CLI output
- Startup log line
- `/api/v1/status` API response

**Alternatives considered**:
- Config-only detection (`mode: team` implies teams edition): Rejected — edition is a build-time property, mode is a runtime property. Personal binary should never accept `mode: team`.

## Docker Image Strategy

**Decision**: Multi-stage Dockerfile using `golang:1.24` builder and `gcr.io/distroless/base` runtime.

**Rationale**: Distroless minimizes attack surface for a server product. Multi-stage keeps the image small (~30MB). Teams-only — personal edition has no Docker use case.

**Alternatives considered**:
- Alpine base: Larger, includes shell (unnecessary for mcpproxy)
- Scratch: Too minimal — no CA certificates, no timezone data
- Ubuntu base: Too large for a single Go binary

## Release Workflow Extension

**Decision**: Add teams matrix entries to existing release.yml. Single release tag, all assets together.

**Rationale**: Minimal CI change. Teams adds 2 Linux matrix entries (amd64/arm64) + Docker build job + deb build job. Same version, same changelog.

**Alternatives considered**:
- Separate release workflow: Rejected — version coordination overhead, maintenance burden
- Separate release tags: Rejected — premature for v1, can revisit if cadences diverge

## Native Tray App Structure

**Decision**: `native/macos/` (Swift) and `native/windows/` (C#) directories with README placeholders.

**Rationale**: Skeleton only for now. The existing Go tray app (`cmd/mcpproxy-tray/`) continues to work until native apps are ready. No build integration yet — native apps will have their own build systems (Xcode, MSBuild).

**Alternatives considered**:
- Separate repos: Rejected by decision — same repo keeps everything together
- `desktop/` directory name: Rejected — `native/` better communicates the intent (platform-native UI)
