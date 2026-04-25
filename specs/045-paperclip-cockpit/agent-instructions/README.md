# Agent Instruction Drafts

Draft instruction files for the 7 Paperclip agents in the MCPProxy cockpit (spec 045). They live in this repo for review **before** being copied into Paperclip's runtime location.

## Workflow

1. Review and edit each file in this directory.
2. Once agents are created in Paperclip (T009/T010/T011/T021/T022/T023/T032 in `tasks.md`), capture each agent's UUID.
3. Copy each subdirectory's contents to:
   `~/.paperclip/instances/default/companies/16edd8ed-8691-4a89-aa30-74ab6b931663/agents/<AGENT_ID>/instructions/`
4. Unpause the agent in the Paperclip Web UI.

## Per-agent file count

- **CEO**: `AGENTS.md`, `SOUL.md`, `HEARTBEAT.md`, `TOOLS.md` (4 files)
- **Critic**: `GEMINI.md`, `AGENTS.md` (2 files — Critic runs on Gemini CLI per FR-015)
- **Other 5 agents**: `AGENTS.md` only (1 file each)

## Format

All `AGENTS.md` files follow a 6-section structure:
**Role / Mandate / Inputs / Outputs / Tools / Provenance**.

The Critic's `GEMINI.md` is identical content to its `AGENTS.md` — Gemini reads `GEMINI.md` preferentially; `AGENTS.md` exists for Paperclip-adapter compatibility.

## Inheritance

The CEO's `TOOLS.md` defines the canonical Synapbus-summarization procedure (FR-016). All other agents reference it by link rather than duplicating.
