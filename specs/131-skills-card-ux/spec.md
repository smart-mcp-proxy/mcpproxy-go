# Feature specification · Skills review card

- **Branch** `codex/skills-card-ux`
- **Created** 2026-11-22
- **Status** Implemented and verified locally

This follow-up to [Spec 125](../125-skills-over-mcp/spec.md) and [Spec 130](../130-skills-review-ux/spec.md) makes the card easier to scan before an approval decision.

## User scenarios and acceptance

### US1 · See scan evidence before approving (P1)

An administrator reviewing a held skill can see the scan verdict and any reported findings before **Approve** or **Approve changes**, on desktop and on mobile. The card explicitly identifies incomplete and unavailable scans. A complete clean scan uses a compact summary. Warnings, dangerous findings, incomplete coverage, and truncated findings retain their existing evidence and caveats.

**Independent test** · Open a pending skill with warnings. Verify the scan evidence precedes the decision controls in document order and appears above them in both layouts.

### US2 · Read the instructions directly (P1)

**Read SKILL.md** in the card opens the current text copy in one action and brings the file viewer into view. The action appears only for a listed text copy of the root `SKILL.md`. Supporting files and approved copies remain accessible through their existing entry points. Closing the viewer returns focus to the action that opened it when still available.

**Independent test** · Open a skill containing `SKILL.md`, activate the shortcut and verify the viewer shows the text. Check supporting files, copy switching, and a binary or missing root file.

### US3 · Expand technical metadata when needed (P2)

**File hashes** is collapsed on a new card and reveals full digests on demand. The file inventory keeps names, sizes and file actions. The comparison shows changed, added and removed files directly. **Unchanged files** is collapsed on a new card and expands to show the unchanged paths.

**Independent test** · Open an approved skill whose files match the reviewed copy. Check both disclosures are closed and expandable. Open a changed skill and verify its changed file, line diff, and approved copy remain accessible.

## Requirements

- **FR-001** · Render scan verdict, coverage notices, and findings before the approval controls. Keep separate messages for complete, incomplete, and unavailable results.
- **FR-002** · Provide a single-action shortcut for the current text `SKILL.md` with viewer focus and keyboard return. Preserve existing digest, offset and copy validation.
- **FR-003** · Collapse file digests and unchanged file paths by default. Keep full digests, changed-file rows, line diffs and approved-copy actions available.
- **FR-004** · Keep review snapshot binding, stale approval guards, authorization, and API behavior as defined in Spec 125.
- **FR-005** · Update Web UI documentation and verify the rendered behavior with unit tests, the isolated Skills browser sweep, and live Hugging Face data at desktop and mobile sizes.

## Success criteria

- **SC-001** · On Hugging Face, the warning and finding evidence for a pending skill appears before its decision buttons at 1440 × 1000 and 390 × 844.
- **SC-002** · One activation opens and focuses the `SKILL.md` viewer with the verified current text at both sizes.
- **SC-003** · Both disclosures are closed on an approved skill card and expand to reveal the exact file digests and unchanged filenames.
- **SC-004** · Existing changed-file, stale-review, blocked, and approved-copy flows pass regression tests.
