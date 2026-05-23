# Publishing MCPProxy to the MCP Registry

This guide covers how to publish (or update) the `server.json` at the repo root to the official [MCP Registry](https://registry.modelcontextprotocol.io).

**Publishing is automated.** The [`.github/workflows/publish-mcp-registry.yml`](../.github/workflows/publish-mcp-registry.yml) workflow publishes `server.json` on every GitHub Release using keyless GitHub OIDC auth — no stored token or secret. It syncs `server.json`'s `version` to the release tag at publish time, so you don't need to hand-bump it. The manual steps below remain useful for first-time setup, validation, ad-hoc `workflow_dispatch` runs, and deprecating versions.

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

- **GitHub authentication**: Only a member/owner of the `smart-mcp-proxy` GitHub org can authenticate for the `io.github.smart-mcp-proxy` namespace. There is no way to delegate or automate this without adding a GitHub Actions workflow with `id-token: write` permission to the release pipeline.
- **Automating via CI** (done): [`.github/workflows/publish-mcp-registry.yml`](../.github/workflows/publish-mcp-registry.yml) runs `mcp-publisher login github-oidc` + `mcp-publisher publish` on `release: published` (and via manual `workflow_dispatch`). It declares `id-token: write`, downloads the pinned `mcp-publisher` binary, syncs `version` from the release tag, validates, then publishes. The OIDC token is minted per run and valid only for that run — no secret is stored. The first run must succeed as a member identity of the `smart-mcp-proxy` org (the workflow's repo identity satisfies this).

## Registry Schema Notes

- `version` must be a plain semantic version string (`0.33.1`) — no `v` prefix, no ranges.
- `packages[]` is empty because none of the supported `registryType` values (`npm`, `pypi`, `oci`, `nuget`, `mcpb`) match MCPProxy's distribution channels (Homebrew tap, GitHub release tarballs, `.deb`/`.rpm`, `go install`). The Docker server-edition image at `ghcr.io/smart-mcp-proxy/mcpproxy-server` is not yet published to releases (gated behind `if: false` in the release workflow).
- `remotes[]` describes the actual MCP transport: a locally-run `mcpproxy serve` instance that exposes a Streamable HTTP endpoint at `http://localhost:8080/mcp`. MCP clients that support HTTP transport connect directly to this address.
- If a Docker image for the server edition is published in a future release, add an `oci` entry to `packages[]` pointing at `ghcr.io/smart-mcp-proxy/mcpproxy-server:{version}` with `"transport": {"type": "stdio"}` and the appropriate `runtimeArguments` for `docker run`.

## References

- [MCP Registry](https://registry.modelcontextprotocol.io)
- [server.json format spec](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/generic-server-json.md)
- [Official registry requirements](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/server-json/official-registry-requirements.md)
- [CLI commands reference](https://github.com/modelcontextprotocol/registry/blob/main/docs/reference/cli/commands.md)
- [JSON Schema](https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json)
