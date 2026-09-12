# Research
Sources: GitHub issues #1265, #1179 and #1184 including its 2026-09-07 triage, verified against origin/main 6b005e949.

- Decision: retain complete nested response for detection and scan parent code_execution before display truncation. Rationale: existing async detector already owns scan bounds. Alternative: enlarge display limits; rejected because it still couples security coverage to presentation.
- Decision: include server identity on detection events, drop unidentified events for scoped subscribers. Rationale: existing event scoping handles this correctly, including serverless parent events. Alternative: remove all detection events; rejected as unnecessarily lossy for authorized subscribers.
- Decision: add enabled metadata to statistics producers and apply ServerContributesTools during scoped recomputation. Rationale: diagnostic per-server counts should remain useful. Malformed server maps return an empty result rather than unfiltered data.
- Decision: admin token routes use owner and name. Rationale: already unique and supported by durable storage operations; hashes are credential material and must not become identifiers.
- Decision: revalidate entitlement during every owned-token validation. Reuse the mint/regenerate entitlement calculation with current user and config. Fail closed if lookup fails. Historical wildcards materialize to current entitlement; explicit scopes only shrink. Ownerless tokens retain operator behavior. Alternative: timer-based revocation; rejected due to stale-access window.
- Decision: do not add a one-time legacy wildcard report. The admin token inventory introduced here already exposes safe `allowed_servers` metadata, so operators can identify literal `"*"` grants without a migration or a second reporting surface.
- Decision: request-time revocation does not cancel work already authorized. Agent-authenticated SSE responses revalidate before each status or runtime event, so unsharing narrows the next frame and revocation closes the stream without a separate connection registry.
- Decision: preserve the maintainer's refutation of #1184 finding 5. Owner-qualified server naming is outside this residual fix.
- Tooling: SpecKit CLI and repository scripts available. OpenCode offers Claude Fable 5, approved by user in place of unavailable 5.1. No ultracode plugin/command found in installed workflow paths.
