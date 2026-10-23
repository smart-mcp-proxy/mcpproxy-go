# Publishing MCPProxy to the MCP Registry

This guide covers how to publish (or update) the `server.json` at the repo root to the official [MCP Registry](https://registry.modelcontextprotocol.io).

**Publishing is already automated** by the `mcp-registry` job in [`.github/workflows/release.yml`](../.github/workflows/release.yml). On every tag/release it authenticates with keyless GitHub OIDC (no stored token/secret), syncs `server.json`'s `version` to the release tag, and publishes — so you never hand-publish a release. The job is `continue-on-error: true`, so a duplicate-version push (the registry stores versions immutably) won't fail the release. The manual steps below remain useful for first-time setup, local validation, and deprecating versions.

## Prerequisites

Install the `mcp-publisher` CLI (macOS/Linux):

```bash
brew install mcp-publisher
```

Verify the install:

```bash
mcp-publisher --help
```

## Authenticate

The namespace `io.github.smart-mcp-proxy` maps to the GitHub organisation `smart-mcp-proxy`. You must be logged in to GitHub as a member of that org (or in a GitHub Actions workflow on one of its repos with `id-token: write`).

**Interactive login (browser):**

```bash
mcp-publisher login github
```

This opens a GitHub OAuth flow and stores a token at `~/.config/mcp-publisher/token.json`.

**CI/CD login (GitHub Actions OIDC — no browser):**

```yaml
permissions:
  id-token: write

- run: mcp-publisher login github-oidc
```

## Validate Before Publishing

Run the validator against the repo-root `server.json` before pushing:

```bash
mcp-publisher validate server.json
```

The tool checks JSON syntax, schema compliance, and registry-specific semantic rules (namespace format, version format, etc.) and reports all issues at once with JSON-path locations.

You can also validate offline using `check-jsonschema`:

```bash
uvx check-jsonschema --schemafile \
  https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json \
  server.json
```

## Publish

From the repo root:

```bash
mcp-publisher publish server.json
```

The CLI will:

1. Re-validate `server.json` against the schema.
2. Submit to `https://registry.modelcontextprotocol.io`.
3. The registry verifies that the authenticated GitHub identity has rights to the `io.github.smart-mcp-proxy` namespace.
4. On success, the entry is live immediately.

## Update an Existing Entry

Bump `version` in `server.json` to match the new release tag (strip the `v` prefix — use `0.34.0` not `v0.34.0`), then re-run `mcp-publisher publish`. Each version is stored as a separate immutable record; the latest version becomes the default.

## Deprecate or Remove a Version

```bash
# Deprecate a specific version
mcp-publisher status --status deprecated \
  --message "Upgrade to 0.34.0" \
  io.github.smart-mcp-proxy/mcpproxy-go 0.33.1

# Hide a version from default listings (e.g. security issue)
mcp-publisher status --status deleted \
  --message "Critical bug fixed in 0.34.0" \
  io.github.smart-mcp-proxy/mcpproxy-go 0.33.1
```

## Packages (what registry-fed catalogs show)

`server.json` carries one `oci` package: the Server-edition image
`ghcr.io/smart-mcp-proxy/mcpproxy-server`, streamable-HTTP at
`http://localhost:8080/mcp`. The registry has **no raw-binary package type**
(`npm`, `pypi`, `nuget`, `cargo`, `oci`, `mcpb` only), so release archives are not
listed; a Claude Desktop `.mcpb` bundle is a separate follow-up
(`specs/121-install-surfaces/followup-mcpb.md`).

How it stays correct on every release (Spec 121):

1. `Dockerfile` and the `build-docker` job label the image
   `io.modelcontextprotocol.server.name=io.github.smart-mcp-proxy/mcpproxy-go`.
   The registry rejects a package whose image lacks this label, and a version
   is immutable once published. A Go test (`cmd/release-gate`) keeps
   `server.json` `name`, the Dockerfile label and the workflow label equal.
2. `scripts/stamp-server-json.sh vX.Y.Z` sets `.version` (no `v`) and the image
   tag in the package identifier (with `v`, as the image is tagged).
   **Both** publishers use it: `release.yml` and `retry-sign-release.yml`.
3. `scripts/registry-preflight.sh vX.Y.Z` runs before `mcp-publisher publish`:
   the tag exists and is anonymously pullable, carries the label, and the
   container started with the exact environment/port `server.json` declares
   answers an MCP `initialize`. A failure skips the publish (the job is
   non-blocking) with the cause in the log.
4. `MCPPROXY_INSTALL_CHANNEL=registry` is declared in the package's
   `environmentVariables`, so installs started from the registry entry report the
   `registry` channel in telemetry.

Versions published before the label existed (≤ 0.71.x) stay package-less; the
first stable release after Spec 121 is the first with a package.
The image is the Server edition (distroless: no shell, Node or uv), so it cannot
launch stdio upstreams; the Personal-edition image of Spec 111 will replace it
in the entry once published.

Local checks: `scripts/stamp-server-json.test.sh`,
`mcp-publisher validate` on the stamped file, and (needs Docker and a published
labelled image) `scripts/registry-preflight.sh vX.Y.Z`.

## What Requires the User

- **Nothing, for the normal release path.** The `mcp-registry` job in [`.github/workflows/release.yml`](../.github/workflows/release.yml) already runs `mcp-publisher login github-oidc` + `mcp-publisher publish` on every tag (`id-token: write`, `continue-on-error: true`). The workflow's repo OIDC identity proves `smart-mcp-proxy` org membership, which owns the `io.github.smart-mcp-proxy` namespace — no secret or interactive login is involved.
- **Manual interactive login** (`mcp-publisher login github`) is only needed for out-of-band actions: validating locally, deprecating/deleting a published version, or a one-off re-publish. Note its browser-issued token is short-lived and expires quickly.

## Registry Schema Notes

- `version` must be a plain semantic version string (`0.33.1`) — no `v` prefix, no ranges.
- `packages[]` is empty because none of the supported `registryType` values (`npm`, `pypi`, `oci`, `nuget`, `mcpb`) match MCPProxy's distribution channels (Homebrew tap, GitHub release tarballs, `.deb`/`.rpm`, `go install`). The Docker server-edition image at `ghcr.io/smart-mcp-proxy/mcpproxy-server` *is* published on every stable tag, but the server edition speaks Streamable HTTP rather than stdio, so it is not a drop-in `oci` package entry either.
- `remotes[]` is intentionally **omitted**. mcpproxy runs locally (`mcpproxy serve` exposes Streamable HTTP at `http://localhost:8080/mcp`), but the registry's semantic validation **rejects localhost/private remote URLs** (`invalid-remote-url`) — `remotes[]` is reserved for publicly reachable hosted endpoints. With no public remote and no installable package type, the entry is **discovery-only**: it lists the server, description, and repository link so clients can find it and follow the repo's install instructions.
- Adding an `oci` entry to `packages[]` for `ghcr.io/smart-mcp-proxy/mcpproxy-server:{version}` would upgrade the entry from discovery-only to one-click installable, but it needs a transport the registry accepts for a container that serves HTTP on a published port — not `"transport": {"type": "stdio"}`. Revisit once the registry's remote/container transport story covers it.

## References

- [MCP Registry](https://registry.modelcontextprotocol.io)
- [server.json format spec](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md)
- [Official registry requirements](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md)
- [CLI commands reference](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/cli/commands.md)
- [JSON Schema](https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json)
