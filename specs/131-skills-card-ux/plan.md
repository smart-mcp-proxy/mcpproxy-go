# Implementation plan · Skills review card

[Spec 131](spec.md) · Branch `codex/skills-card-ux` · 2026-11-22

## Design

1. Move stored scan evidence in `SkillDetail.vue` before decision controls. Use one compact verdict line for a complete clean scan and an emphasized panel for warnings, danger, incomplete coverage, or a missing verdict.
2. Offer **Read SKILL.md** beside the decision controls when the current root file is text. Route it through the existing paged viewer. Bringing focus to the viewer also covers file-list and approved-copy entry points. Preserve focus on copy switches and return it on Close.
3. Show full digests in a native **File hashes** disclosure. Partition the existing file diff by state. Render changed rows with their current actions and put unchanged paths in a native disclosure.
4. Check the warning order and disclosure states in Vitest and Playwright. Run the normal Skills sweep against an isolated embedded build. Check pending and approved Hugging Face skills at desktop and mobile widths using read-only calls.
5. Update [Skills over MCP](../../docs/features/skills.md) and [Server Detail](../../docs/web-ui/server-detail.md).

## Constitution check

This is a Vue presentation change. The existing scanner, server state, buffer limits, API routes, and approval policy supply the data and decisions. The viewer requests data on demand and continues to validate digest and offset. Focused regression tests cover changed behavior before the browser sweep. The feature docs describe the interaction.

## Validation commands

- `npm run test -- --run` from `frontend/`
- `scripts/run-skills-tab-sweep.sh --out <artifact-dir> --report-html <artifact-dir>/report.html`
- `SPECIFY_FEATURE=131-skills-card-ux .specify/scripts/bash/check-prerequisites.sh --json --require-tasks --include-tasks`
