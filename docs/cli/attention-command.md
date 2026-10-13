---
id: attention-command
title: Attention Command
sidebar_label: Attention Command
sidebar_position: 6
description: "mcpproxy attention prints the one needs-attention list every MCPProxy surface shares, and the same count leads mcpproxy status and mcpproxy doctor"
keywords: [attention, needs attention, status, doctor, review, sign in, cli]
---

# Attention Command

`mcpproxy attention` prints the **one** needs-attention list that the Web UI Home page, the macOS tray and Home section, and the CLI all read from `GET /api/v1/attention`. The count and the order are identical on every surface. See [Needs Attention](/features/needs-attention) for the item kinds, ranks and fixes.

```
mcpproxy attention [-o table|json|yaml]
```

The command is a **report, not a check**: it exits 0 whether the list is empty or not. Use `mcpproxy doctor` for a diagnostic pass. It needs a running daemon (`mcpproxy serve`).

## Table output

```bash
mcpproxy attention
```

```
#  KIND              SUBJECT  SUMMARY                          FIX
1  sign_in_required  github   github: sign in required         Sign in
2  server_review     github   github: waiting for review       Review
3  missing_secret    weather  weather: missing secret          Add secret
```

Items are ordered by rank (a sign-in item comes before the review item of the same server), then by subject name. With nothing to do the command prints `All clear`.

## JSON output

`-o json` prints the `data` object of the REST response: `count`, `generated_at` and `items[]`, where each item carries `id`, `kind`, `rank`, `subject {type, id, name}`, `summary`, `detail`, `fix {verb, label, target}` and `since`.

```bash
mcpproxy attention -o json | jq -r '.items[].id'
```

## The same list in `status` and `doctor`

| Command | Where the list appears |
|---|---|
| `mcpproxy status` | The first line after the header: `Needs attention: 3 (run 'mcpproxy attention')`, or `Needs attention: none` |
| `mcpproxy doctor` | The first section, `Needs attention (3)`, listing each item as `[kind] summary (fix label)`. The diagnostics that follow are headed `Diagnostics: N findings`; the old "issues that need attention" wording is retired |

An older daemon without the endpoint omits the line and the section rather than printing a count of 0.

## Fixing an item

Each item's fix is a navigation, never a one-click change: `Sign in` opens the server, `Review` opens the review screen, `Add secret` opens the server's secret form. From the CLI the matching commands are:

```bash
mcpproxy auth login --server=github     # sign in
mcpproxy review show github             # inspect the captured tools
mcpproxy review approve github          # approve through the scan gate
```

See [Review Commands](/cli/review-commands) for the review workflow and [Status Command](/cli/status-command) for the full status output.
