# Contract: REST API (`/api/v1`)

Authentication as for the rest of the API. "Admin" = the API key, the socket, or an admin user; a scoped agent token is not admin.

## Who sees what (all skill reads)

A skill is **available** when the feature is on, its server is enabled, not quarantined and not excluded (`expose_skills: false`), and the skill is approved, not blocked and listed. An admin sees every skill of a server it can see. Any other caller sees available skills of servers in its reach and nothing else: no entry, no count, no review or attention row, no event for a skill that is not available. This holds on the endpoints below and on the existing review, attention and SSE endpoints (from PR-2).

## `GET /servers/{id}/skills`

- Admin: every record of the server.
- Scoped caller with the server in reach (from PR-3): only skills that are available to agents, and no counts of the others.
- Server outside reach: 404, as for the server itself.
- `enable_skills` off: 200 with `{"enabled": false, "skills": []}`.

```json
{
  "enabled": true,
  "server": "hf",
  "supported": true,
  "expose_skills": null,
  "last_refresh": "2026-11-16T19:20:00Z",
  "error": "",
  "truncated": false,
  "skills": [
    {
      "id": "hf:hf-cli",
      "uri": "skill://hf-cli/SKILL.md",
      "path": "hf-cli",
      "name": "hf-cli",
      "description": "…",
      "status": "pending",
      "blocked": false,
      "listed": true,
      "available": false,
      "unusable_reason": "",
      "current_hash": "sk1-…",
      "approved_hash": "",
      "files": [{"path": "SKILL.md", "size": 34197, "digest": "sha256:…", "text": true}],
      "total_size": 34197,
      "scan": {"verdict": "", "complete": false, "findings": []}
    }
  ]
}
```

## `GET /servers/{id}/skills/content?skill=<path>&file=<relpath>&copy=current|approved&offset=<n>&limit=<n>`

Admin only until PR-3; afterwards a scoped caller may read the approved copy of an available skill. `file` defaults to `SKILL.md`, `copy` to `current` for admin and is forced to `approved` for a scoped caller. Returns `{"text", "offset", "next_offset" (absent at the end), "size", "digest"}`. 404 for an unknown skill or file, 415 for a binary file.

## `POST /servers/{id}/skills/refresh`

Admin only. Starts a refresh and returns the listing once applied, or 409 `refresh_in_progress` joined to the running one. 409 `skills_not_supported` when the server does not announce the extension.

## `POST /servers/{id}/skills/approve` (PR-2)

Admin only. Body: `{"skills": ["hf-cli"], "expected_hashes": {"hf-cli": "sk1-…"}, "expected_incarnation": 3}`.

- 200 `{"approved": 1}`.
- 409 `review_out_of_date` with `{changed: [], unreviewed: []}` when any current hash differs from the expected one; nothing is written.
- 409 `server_replaced` when the incarnation differs.
- 422 for an unusable or dynamic skill.

## `POST /servers/{id}/skills/block`, `…/unblock` (PR-2)

Admin only. Body: `{"skills": ["hf-cli"]}`. 200 with the changed count.
