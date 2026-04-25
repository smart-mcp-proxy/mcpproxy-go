# mcpproxy-architecture-decisions

> **Maintained by**: Paperclip CEO agent (appended on each synthesis where the user approved a non-default option B/C over A)
> **Format**: append-only log; entries are immutable once written
> **Cadence**: one entry per non-default routing decision

## Format for new entries

```markdown
## YYYY-MM-DD — <decision title>

**Goal**: Paperclip ticket #N — <one-line summary>
**Options considered**: A — <approach>; B — <approach>; C — <approach>
**Decision**: B (or C, or "user requested change to D")
**Rationale**: <why, citing tradeoffs>
**Sources**: [#open-brain msg #12345], [[mcpproxy-roadmap]] entry 2026-MM-DD, prior PR #NNN
```

## Entries

(empty — first entry will be appended when the cockpit ships its first non-default-option goal)

---

**When to append**: every time the user reacts `approve` on a synthesis where Option A was NOT the recommendation, OR where the user picked B/C over CEO's recommended A. The point of this article is durable institutional memory of *why* we chose differently from the obvious default.

**When NOT to append**: routine decisions that picked the obvious option, trivial bug fixes, or anything where the synthesis only had one option.
