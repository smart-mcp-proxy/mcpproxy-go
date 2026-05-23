# Publishing MCPProxy to the MCP Registry

This guide covers how to publish (or update) the `server.json` at the repo root to the official [MCP Registry](https://registry.modelcontextprotocol.io). This is a manual step that requires authentication as the namespace owner — it cannot be automated without a GitHub OIDC workflow scoped to the `smart-mcp-proxy` org.

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
- **Automating via CI**: To run `mcp-publisher login github-oidc` + `mcp-publisher publish` in the release workflow, add a new job after the `release` job in `.github/workflows/release.yml`, grant `id-token: write` in its `permissions` block, and run the two commands against the tagged `server.json`. The OIDC token is valid for the duration of the workflow run only.

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
