# Paperclip Agent Cockpit (overview)

A subset of MCPProxy work flows through a Paperclip agent cockpit (CEO + 6 expert agents) running on the developer's local machine. The cockpit is configured outside this repo (in `~/.paperclip/instances/` and Synapbus on `kubic.home.arpa`).

For details:

- **Spec**: [`specs/045-paperclip-cockpit/`](../specs/045-paperclip-cockpit/)
- **Design**: [`docs/superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md`](superpowers/specs/2026-04-25-paperclip-goal-cockpit-design.md)
- **Bootstrap walkthrough**: [`specs/045-paperclip-cockpit/quickstart.md`](../specs/045-paperclip-cockpit/quickstart.md)

## Why this exists

Direct Claude Code sessions remain the daily tactical tool for brainstorming, investigation, and trivial fixes (no change there). For higher-level goals — strategic direction, multi-area features, telemetry-driven product decisions — the cockpit decomposes the goal across expert agents, has a Critic adversarially review proposals, and produces a single recommendation for user approval. After approval, implementation flows autonomously to a PR (with a speckit spec for big features) — but never auto-merges. Human PR review remains mandatory.

## Multi-LLM design

| Agent | CLI |
|---|---|
| CEO + Backend + Frontend + macOS + Release + QA | Claude Code |
| Critic | Gemini CLI (`gemini-3.1-pro-preview`) — model diversity for adversarial review |
| Synapbus context summarization | opencode + `kimi2.5-gcore` — long-context offload |

## Where to send a goal

If you're a contributor wondering where to file something:

- **Bug fix or one-file change**: skip the cockpit; open a PR directly. (The cockpit's overhead isn't worth it for trivial work.)
- **Multi-area feature, design question, telemetry-driven research**: file as a Paperclip CEO ticket on `http://127.0.0.1:3100`.
- **Brainstorm or investigation**: stay in your interactive Claude Code session. The cockpit doesn't replace that.

The CEO's spec-vs-no-spec routing rules live in `specs/045-paperclip-cockpit/agent-instructions/ceo/SOUL.md`.
