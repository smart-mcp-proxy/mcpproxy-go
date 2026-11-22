# Verification: Skills review navigation

Use an administrator session and a skills server with pending, changed, approved, blocked, unusable, and no-longer-listed records. A 25-skill fixture catches long-list layout regressions.

1. From the global Review queue, open Review on a skill-only row. Confirm `?tab=skills&state=needs-review`, Needs review active, and only pending/changed currently listed unblocked records shown; compare count with the list.
2. From a mixed row, open Skills and confirm the same filter. Open its Review action and confirm tool review still opens.
3. Open `/ui/servers/<name>?tab=skills` directly. Confirm All skills is active. Try an unknown `state` and confirm it also shows All skills.
4. On desktop, select the first skill of a 25-item list; its detail heading appears beside the list in the current viewport.
5. At 390 px width, select a skill with keyboard. Confirm detail replaces the list, receives focus, and Back to skills restores focus to the same row. Check horizontal overflow.
6. While detail is open, refresh unchanged content; the card stays open. Change its hash or server incarnation and refresh; approval remains disabled until Open the new version. Approve or block a selected held skill; its detail stays visible even if it leaves the filter.
7. Check empty Needs review, failed refresh, removed skill, direct links, and non-administrator access.

The [local verification report](verification/report.html) records the embedded Web UI sweep and live Hugging Face checks.
