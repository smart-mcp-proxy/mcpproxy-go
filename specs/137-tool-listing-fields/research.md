# Research: Preserve the New Tool Listing Fields

## R1. Hold the tool, or withhold the title?

- **Decision**: withhold the title; never hold the tool for a title.
- **Rationale**: the threat a title adds is text reaching a model. Withholding the text removes the threat completely while leaving the reviewed contract (description, schemas, hints, dispatch tier) callable. Holding the tool would turn every cosmetic rename into an outage and, at upgrade, would hold every tool whose upstream already sends a title.
- **Alternatives**: (a) fold the title into the rug-pull hash v6 and hold on change: needs a hash migration and mass-holds on upgrade in manual mode; rejected. (b) a Spec 120 structural signal: that gate exists for capability widening, which a title is not; rejected. (c) pass titles through unreviewed: reopens a quarantine bypass and keeps the existing `annotations.title` gap; rejected.

## R1a. Title scan

- **Decision**: scan each title on its own (title-only view), never inside the contract scan.
- **Rationale**: the contract gate holds a tool on any per-tool finding, and the phrase detector returns one match over the joined text with no field attribution. Mixing titles in would let a title finding hold a contract and let a description finding be blamed on (or hide behind) a title.

## R2. Rug-pull hash and fingerprint

- **Decision**: keep the v5 approval hash; bump the review fingerprint to `rv3` with the live titles.
- **Rationale**: no stored hash changes, so no tool can change status on upgrade (SC-002). The fingerprint is derived, never stored; a version bump only makes an open review page reload.
- **Alternatives**: a separate title fingerprint and a separate approval door: more surface for the same guarantee; rejected. Letting unbound approvals approve titles: the agent-reachable `quarantine_security` approve operations are unbound, so an agent could approve text it then reads; rejected.

## R3. `annotations.title` at upgrade

- **Decision**: a one-time pass before the first discovery adopts each record's **stored** `annotations.title` (the value the previous version was emitting); nothing live is grandfathered, new records get nothing, and a record that later loses its title state to an older binary fails closed.
- **Rationale**: the stored value is what agents saw before the upgrade, so adopting them does not increase exposure. Scanning them at upgrade and withholding flagged ones would be stricter, but would make the upgrade change what agents see for a reason the operator did not act on. Tightening applies from the next change. Recorded as spec Assumption 2 so a reviewer can overturn it.

## R4. Icons

- **Decision**: store and forward validated icons; never fetch; no review.
- **Rationale**: MCP's own security guidance for icons is that consumers must reject unsafe schemes and treat icon data as untrusted. mcpproxy forwards `https` and image `data:` URIs only, caps their size, and the Web UI does not load remote icons unless asked (privacy: an upstream-chosen host would otherwise learn when an administrator opens the page). Models do not read icons, so they add no injection surface.
- **Alternatives**: proxy and re-host icons (a fetcher with SSRF exposure: rejected, out of scope); drop `https` icons (loses most real icons: rejected); allow `http:` (mixed content and tracking: rejected).

## R5. `_meta`

- **Decision**: opaque pass-through on the direct surface only, capped at 16 KiB, mcpproxy-owned keys win.
- **Rationale**: MCP reserves `_meta` for protocol and host extensions; mcpproxy already uses tool `_meta` itself (`anthropic/maxResultSizeChars`, the internal stamp). Agent text surfaces (`retrieve_tools`, `describe_tool`) are mcpproxy's own and gain nothing from it.
- **Alternatives**: hashing `_meta` (holds tools on version/timestamp churn); dropping it (breaks host extensions keyed on tool `_meta`); an allowlist of keys (no stable registry to allowlist against). A global off switch is a possible follow-up.

## R6. `defer_loading`

- **Decision**: forward on the direct surface; no effect on `retrieve_tools`.
- **Rationale**: on the aggregated surface every upstream tool is already deferred behind `retrieve_tools`; the flag carries no relevance information. On the direct surface the client is the one that loads tools, so the upstream's hint is forwarded unchanged. The name is not in the published MCP schema; mcp-go uses the snake_case spelling of the Anthropic API's tool-search flag. Treated as a hint (spec Assumption 3).

## R7. `execution.taskSupport`

- **Decision**: capture and display; never forward until #2043.
- **Rationale**: mcpproxy does not declare the tasks capability and cannot carry a task-augmented call. Forwarding `required` would direct a client to a path that fails. Displaying it lets administrators see which tools cannot be used through mcpproxy yet.

## R8. Size caps

- **Decision**: 256-character titles, 8 icons, 32 KiB per data URI, 64 KiB icons per tool, 16 KiB `_meta`, 2 MiB icons plus `_meta` per server.
- **Rationale**: typical listings carry no icons or one URL; data-URI icons are usually a few KiB. The per-server budget bounds a 3,000-tool fleet's growth to a few MiB in the worst realistic case, instead of ~240 MB at per-tool caps alone.
