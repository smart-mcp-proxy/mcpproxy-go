## 🔒 Colon-named tools are approved by their exact name — one-time review after upgrade

A tool's approval record, search-index entry and callability are now keyed by the **exact name its server reports**, colons included. Earlier releases filed a namespaced tool such as `ns:erase` under the text after its first colon, so it shared — and silently inherited — the approval of a sibling `erase` on the same server. Every dispatch path (`call_tool_*`, direct-name dispatch on `/mcp/all`, `call_tool()` inside code execution), preflight and `describe_tool` now resolve exactly the `server:tool` pair they dispatch.

**What changes for you**

- **On `manual` (default) and `scan` trust, colon-named tools on a server that already has an approved baseline become pending once, under their own names, on the first discovery after upgrade.** They stay uncallable and out of `retrieve_tools` until you approve them: `mcpproxy upstream inspect <server>` to review, `mcpproxy upstream approve <server> <tool>`, the `quarantine_security` MCP tool, or the Web UI. `trust_mode: auto` servers and installs with `quarantine_enabled: false` auto-approve them; no other tool is affected.
- **Blocks carry over.** A tool you had disabled — or that was locked pending review — under the old collapsed name stays blocked under its own name; the log records the carry-over at `WARN` with both names. Nothing is deleted.
- **Unresolved names are refused for everyone.** A call to a tool that a connected server's discovered tool set does not contain is refused before any upstream call, for administrators too — refresh with `retrieve_tools` and retry with a listed name. Quarantined, disabled and disconnected servers keep their existing answers.

Details: [Security Quarantine → Namespaced tool names](https://docs.mcpproxy.app/features/security-quarantine#namespaced-tool-names) and [Agent Tokens → Target tool tier](https://docs.mcpproxy.app/features/agent-tokens#target-tool-tier).
