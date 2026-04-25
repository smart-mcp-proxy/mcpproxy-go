# mcpproxy-shipped

> **Maintained by**: Paperclip CEO agent (appended on each PR-merge linked to a Paperclip ticket)
> **Format**: append-only log
> **Cadence**: one entry per merged PR

## Format for new entries

```markdown
## YYYY-MM-DD — <PR title>

**PR**: #NNN ( https://github.com/smart-mcp-proxy/mcpproxy-go/pull/NNN )
**Goal**: Paperclip ticket #N — <one-line summary>
**Spec**: [[spec-NNN-shortname]] (or "no spec — small route")
**Summary**: <one-paragraph "what changed and why">
**QA report**: <link to HTML report attached to the Paperclip ticket, if any>
```

## Entries

(empty — first entry will be appended when the cockpit ships its first PR)

---

**When to append**: every time a PR opened by a cockpit implementation expert merges to `main`. CEO heartbeat detects merges via gh polling or webhook.

**Anti-spam**: at most one entry per merged PR. Re-merges or amend-and-force-push do NOT generate new entries.
