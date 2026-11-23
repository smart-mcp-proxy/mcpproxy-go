# Quickstart: Skills over MCP

## Try it (after PR-1)

1. Turn the feature on in `~/.mcpproxy/mcp_config.json`:

   ```json
   { "enable_skills": true }
   ```

2. Add a server that serves skills, for example the public Hugging Face server:

   ```bash
   mcpproxy upstream add hf https://huggingface.co/mcp
   ```

3. List what it offers:

   ```bash
   mcpproxy skills list --server hf
   ```

4. Read one:

   ```bash
   mcpproxy skills show hf:hf-cli
   ```

Every skill is `pending` at this point; no agent can see any of them.

## Review and approve (after PR-2)

```bash
mcpproxy skills show hf:hf-cli --diff     # what changed since the last approval
mcpproxy skills approve hf:hf-cli         # bound to what `show` displayed
mcpproxy skills block hf:hf-cloud-aws-context-discovery
```

## Use from an agent (after PR-3)

In any connected client, the agent calls `retrieve_skills` with a phrase, then `load_skill` with the id it got back.

## Developer loop

```bash
go test -race ./internal/skills/... ./internal/upstream/... ./internal/storage/... ./internal/runtime/...
go test ./internal/httpapi/ -run Skills
go test ./cmd/mcpproxy/ -run Skills
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml --build-tags server ./...
make swagger   # after any @Param or config.Config change
```

The fixture upstream is `internal/skills/skillstest`; tests never contact a public server.

## Manual check against a real server

With a throwaway data directory and a high port (never the default instance):

```bash
./mcpproxy serve --config=<scratch>/cfg.json --listen=127.0.0.1:18095
curl -s -H "X-API-Key: <scratch key>" http://127.0.0.1:18095/api/v1/servers/hf/skills | jq '.data.skills | length'
```

Expected: 25 (as of 2026-11-16), each with `status: "pending"` and matching digests.
