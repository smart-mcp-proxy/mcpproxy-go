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

## What Requires the User

- **Nothing, for the normal release path.** The `mcp-registry` job in [`.github/workflows/release.yml`](../.github/workflows/release.yml) already runs `mcp-publisher login github-oidc` + `mcp-publisher publish` on every tag (`id-token: write`, `continue-on-error: true`). The workflow's repo OIDC identity proves `smart-mcp-proxy` org membership, which owns the `io.github.smart-mcp-proxy` namespace — no secret or interactive login is involved.
- **Manual interactive login** (`mcp-publisher login github`) is only needed for out-of-band actions: validating locally, deprecating/deleting a published version, or a one-off re-publish. Note its browser-issued token is short-lived and expires quickly.

## Registry Schema Notes

- `version` must be a plain semantic version string (`0.33.1`) — no `v` prefix, no ranges.
- `packages[]` is empty because none of the supported `registryType` values (`npm`, `pypi`, `oci`, `nuget`, `mcpb`) match MCPProxy's distribution channels (Homebrew tap, GitHub release tarballs, `.deb`/`.rpm`, `go install`). The Docker server-edition image at `ghcr.io/smart-mcp-proxy/mcpproxy-server` is not yet published to releases (gated behind `if: false` in the release workflow).
- `remotes[]` is intentionally **omitted**. mcpproxy runs locally (`mcpproxy serve` exposes Streamable HTTP at `http://localhost:8080/mcp`), but the registry's semantic validation **rejects localhost/private remote URLs** (`invalid-remote-url`) — `remotes[]` is reserved for publicly reachable hosted endpoints. With no public remote and no installable package type, the entry is **discovery-only**: it lists the server, description, and repository link so clients can find it and follow the repo's install instructions.
- If a Docker image for the server edition is published in a future release, add an `oci` entry to `packages[]` pointing at `ghcr.io/smart-mcp-proxy/mcpproxy-server:{version}` with `"transport": {"type": "stdio"}` and the appropriate `runtimeArguments` for `docker run`. That would upgrade the entry from discovery-only to one-click installable.

## References

- [MCP Registry](https://registry.modelcontextprotocol.io)
- [server.json format spec](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md)
- [Official registry requirements](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md)
- [CLI commands reference](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/cli/commands.md)
- [JSON Schema](https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json)
