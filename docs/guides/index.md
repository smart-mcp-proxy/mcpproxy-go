---
title: Guides
slug: /guides
description: "Complete MCPProxy tasks: share one setup across clients, diagnose unavailable tools, discover tools on demand, and review tool definitions before use."
---

# What do you want to do?

These guides take you from a specific problem to a result you can verify.
New to MCPProxy? [Install it first](/getting-started/installation), then follow the [Quick Start](/getting-started/quick-start).

| Your task | What you will finish with |
| --- | --- |
| [Reuse one MCP setup in two AI clients](/guides/two-clients-one-setup) | Claude Code and Codex using the same upstream, with a successful call from each recorded in Activity. |
| [Find out why an agent cannot use a tool](/guides/diagnose-unavailable-tool) | A diagnosed connection, approval, access or call failure, followed by a verified retry. |
| [Discover tools on demand](/guides/discover-tools-on-demand) | A measured comparison of initial tool definitions and a successful call through discovery. |
| [Review a tool before use](/guides/review-tool-before-use) | A deliberate approval decision for a new server and a changed tool definition. |

## Choose the right reference

- **How tools reach the agent:** [Routing modes](/features/routing-modes) explains discovery, direct tools and code execution. The two-client guide uses the default `retrieve_tools` mode.
- **What a client can access:** [Profiles](/features/profiles) and [agent tokens](/features/agent-tokens) cover access restrictions.
- **What is held for review:** [Security quarantine](/features/security-quarantine) explains server admission and tool changes.
- **Exact flags and fields:** use the [CLI](/cli/command-reference), [configuration](/configuration/config-file) and [REST API](/api/rest-api) references.
