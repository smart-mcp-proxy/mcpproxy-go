# Follow-up note: Claude Desktop `.mcpb` bundle (NOT part of Spec 121)

Status: open questions only. Nothing here is implemented in the E7 PR.

## Why it matters
Claude Desktop is 18% of actives, today reached through an `mcp-remote` bridge. An `.mcpb` bundle gives one-click install in Claude Desktop, and the registry's `mcpb` package type (identifier = release-asset URL, mandatory `fileSha256`, URL must contain "mcp") would give registry-fed catalogs a second install action.

## Open questions
1. **What does the bundle wrap?** (a) A stdio bridge binary that connects to an already-running local MCPProxy (needs it running: how does a one-click install start the core?), (b) the full core as a stdio server (Claude Desktop owns its lifecycle; conflicts with the tray-owned core, the DB lock on `~/.mcpproxy` and port 8080), (c) a launcher that starts/ensures the tray core then bridges. Recommended starting point: (c), reusing the existing `mcp-remote`-equivalent bridge logic in the Connect wizard.
2. **Platforms**: mcpb bundles are per-OS/arch (or multi-binary with a manifest platform map). macOS arm64/amd64, Windows amd64/arm64 at minimum; Linux unsupported by Claude Desktop.
3. **Signing / notarization**: bundle contains executables; macOS needs the same notarized binary as the DMG. Can the release job reuse the already signed artifacts instead of signing again?
4. **Auth**: the bridge must send `X-API-Key` (or use the Unix socket); where does it read the key — config file at runtime — and how does that interact with quarantine/agent tokens (a scoped agent token per bundle?).
5. **user_config** in the manifest: expose port and optional token vs read local config; privacy of values.
6. **Updates**: the bundle is versioned independently of the core; does the bundle auto-update with Claude Desktop's mcpb update flow, and what is the version-skew contract with the running core?
7. **Registry**: adding an `mcpb` entry to `packages` requires the release asset URL and SHA-256 to be known before publish; ordering with the `release` job.
8. **Telemetry**: add `mcpb` to the install-channel enum (schema bump) and have the bundle declare it through the channel marker.
9. **Interaction with Connect wizard**: the wizard currently writes the `mcp-remote` entry for Claude Desktop; decide whether the bundle replaces or complements it.
