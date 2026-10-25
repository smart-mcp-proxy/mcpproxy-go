# Research — Spec 121

| Topic | Decision | Evidence / alternatives |
|---|---|---|
| Registry package type | `oci` → `ghcr.io/smart-mcp-proxy/mcpproxy-server:vX.Y.Z` | Registry package types: npm, pypi, nuget, cargo, oci, mcpb (modelcontextprotocol.io/registry/package-types). No binary type. `mcpb` deferred (followup-mcpb.md). |
| OCI ownership | Image label `io.modelcontextprotocol.server.name` = server name | Same doc. Current image `v0.71.1` labels checked via `docker inspect`: only OCI-standard labels present → label is required before publishing. Image is anonymously pullable (token endpoint returns a token; `docker pull` unauthenticated works). |
| Image behaviour | Starts, serves `/mcp` on 8080, MCP session registers with no config | Ran `v0.71.1` locally on 127.0.0.1:18391 (see log). Distroless, so it cannot launch stdio upstreams → catalog description must say so; Personal image (Spec 111) is the follow-up. |
| Transport entry | `streamable-http`, url `http://localhost:8080/mcp` | LocalTransport schema allows streamable-http with URL. |
| Registry publishers | Two: release.yml and retry-sign-release.yml | Both only `jq .version`; shared script required. |
| Image tag spelling | tag includes `v`; registry version does not | `release.yml` build-docker `tags:` uses `github.ref_name`. |
| Winget installer type | `inno` signed installer | `scripts/installer.iss` is Inno; assets `mcpproxy-setup-v<ver>-<arch>.exe`; sha256 in `checksums.txt`. Portable zip rejected (loses tray/Start-menu/PATH). |
| Winget automation | SHA-pinned winget-releaser on ubuntu | Alternative `wingetcreate update --submit` needs Windows runner; rejected for cost. First PR must be manual. |
| Scoop source | Release zip `mcpproxy-<ver>-windows-<arch>.zip` (flat: mcpproxy.exe, mcpproxy-tray.exe) | Verified by downloading v0.71.1 zip. Hashes from `checksums.txt` (covers all assets). |
| Scoop self-update risk | New `scoop` channel; zip is stamped `tarball` which would otherwise allow self-update | `release.yml:432-449`, `update_cmd.go decideAction`. |
| Install channel telemetry | Reuse `updatecheck` detector + resolver with declarations | Detector exists since Spec 079/092 and is already in `/api/v1/info`. |
| Schema bump | None | Additive, always populated; siblings (118/119 specs) may bump concurrently → collision. |
| llms.txt | Already generated | `curl https://docs.mcpproxy.app/llms.txt` → 200, 39 KB; llms-full 2.9 MB; config `docusaurus-plugin-llms` at `website/docusaurus.config.js:92`. Tune, don't replace. |
| Connect CLI | `mcpproxy connect <client>`; clients: claude-code, cursor, windsurf, vscode, codex, gemini, opencode, zcode; no dry-run; Claude Desktop only via wizard | `mcpproxy connect --help`. |
