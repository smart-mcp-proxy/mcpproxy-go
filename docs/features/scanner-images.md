# Security Scanner Images

MCPProxy's security scanners run as Docker containers. This document
explains where those images come from, how they're published, and why we
keep them in this repository instead of a separate one.

## Image sources

| Scanner | Image | Source |
|---|---|---|
| `cisco-mcp-scanner` | `ghcr.io/smart-mcp-proxy/scanner-cisco:latest` | Custom wrapper in `docker/scanners/cisco/` |
| `mcp-ai-scanner` | `ghcr.io/smart-mcp-proxy/mcp-scanner:latest` | Built from the separate [`smart-mcp-proxy/mcp-scanner`](https://github.com/smart-mcp-proxy/mcp-scanner) repo (AI scanner needs its own release cycle) |
| `mcp-scan` (Snyk) | `ghcr.io/smart-mcp-proxy/scanner-snyk:latest` | Custom wrapper in `docker/scanners/snyk/` |
| `nova-proximity` | `ghcr.io/smart-mcp-proxy/scanner-proximity:latest` | Custom wrapper in `docker/scanners/proximity/` |
| `ramparts` | `ghcr.io/smart-mcp-proxy/scanner-ramparts:latest` | Custom wrapper in `docker/scanners/ramparts/` |
| `semgrep-mcp` | `returntocorp/semgrep:latest` | **Vendor image** — maintained upstream by Semgrep |
| `trivy-mcp` | `ghcr.io/aquasecurity/trivy:latest` | **Vendor image** — maintained upstream by Aqua Security |

Rule of thumb:

1. **Prefer vendor images.** Trivy and Semgrep ship their own high-quality
   multi-arch images. Wrapping them in our own Dockerfile would only add
   lag between upstream releases and MCPProxy users.
2. **When no vendor image exists, publish our own thin wrapper** under
   `ghcr.io/smart-mcp-proxy/scanner-<id>:latest`. A wrapper installs the
   vendor CLI from PyPI / crates.io and adds an entrypoint that reads from
   `/scan/source` and writes SARIF to `/scan/report/results.sarif`.

## Why one repository, not a separate one

An earlier idea was to keep all scanner Dockerfiles in a separate
`smart-mcp-proxy/scanners` repo. We decided against that for three reasons:

1. **Version drift.** The registry that MCPProxy ships (`registry_bundled.go`)
   and the image tags it expects must move together. Keeping the Dockerfile
   and the Go constant in the same commit makes that trivial; splitting
   them across repos means every scanner change becomes a two-repo dance
   with two PRs and two approvals.

2. **Single CI story.** Releases already build the MCPProxy binary, the
   macOS DMG, and the Docker image for the server edition. Adding the
   scanner images to the same repository means one workflow
   (`scanner-images.yml`), one set of secrets, one place to debug.

3. **Small surface.** Each wrapper is ~30 lines of Dockerfile plus a tiny
   entrypoint. There simply isn't enough code to justify an extra repo.

The one exception is the **AI scanner** (`mcp-ai-scanner`) — it lives in
[`smart-mcp-proxy/mcp-scanner`](https://github.com/smart-mcp-proxy/mcp-scanner)
because the agent logic there is non-trivial and has its own release
cadence. We still reference the published image name from here.

## Publishing

The `.github/workflows/scanner-images.yml` workflow builds and pushes all
four wrappers whenever:

- A commit to `main` touches `docker/scanners/**` or the workflow itself.
- A maintainer triggers it manually via `workflow_dispatch` (optionally for
  a single scanner id).

Pull requests run the build step but do **not** push, so Dockerfile drift
is caught in CI without leaking images from forks.

Images are multi-arch (`linux/amd64` + `linux/arm64`), tagged with both
`:latest` and a short-SHA tag for pinning.

## Background image pull UX

MCPProxy pulls these images lazily:

- When the user toggles a scanner on (`POST /api/v1/security/scanners/{id}/enable`),
  the Docker pull runs in a background goroutine. The scanner status
  immediately moves to `pulling`; the web UI shows a spinner and reacts
  to `security.scanner_changed` SSE events.
- When the pull finishes, status flips to `installed` (no env config) or
  `configured` (API key already set).
- If the pull fails, status becomes `error` with the reason in
  `error_message`, and a "Retry" button in the Security page calls the
  enable endpoint again.
- When a user changes the Docker image override via
  `PUT /api/v1/security/scanners/{id}/config`, the service detects that the
  effective image changed and kicks off a new background pull.
- Scanners with a missing image are no longer silently dropped from a
  scan. They are recorded as a failed scanner in the scan report so users
  can see exactly which scanner didn't run and why.

## Adding a new scanner

1. Write a Dockerfile in `docker/scanners/<id>/Dockerfile`.
   Expect `/scan/source` (read-only) for input and `/scan/report` (writable)
   for output.
2. If the scanner needs custom argv, add an entrypoint script next to it.
3. Append an entry to `registry_bundled.go` pointing at
   `ghcr.io/smart-mcp-proxy/scanner-<id>:latest`.
4. Add the matrix entry to `.github/workflows/scanner-images.yml`.
5. Land the change in a single PR so all three moving parts stay in sync.
