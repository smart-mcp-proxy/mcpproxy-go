package httpapi

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// The /events door, second half of #1166.
//
// Scoping the servers.changed EMBED (see renderEventPayloadForCaller) closed
// one window in one room. The event bus publishes twenty-odd types and most of
// them name a server in a scalar field, so a token scoped to `alpha` still
// learned, live, that `beta` exists, that it was just disabled, what its
// previous and new state were, when its OAuth refresh failed and what a
// security scan found on it. Observed on the wire by a token scoped to alpha,
// on a second subscriber, while an admin was watching the same stream:
//
//	data: {"payload":{"action":"server_disabled","affected_entity":"beta",
//	       "changed_fields":["enabled"],"new_values":{"enabled":false},
//	       "previous_values":{"enabled":true},"source":"api"},...}
//
// That is the same disclosure #1166 is about — the name, the state and the
// mutation history of a server outside the caller's scope — reaching the same
// door through a different event type.

// eventServerIdentityFields are the runtime-event payload keys whose VALUE is
// the name of an upstream server. Enumerated from every publishEvent call site
// in internal/runtime (event_bus.go, tool_quarantine.go, lifecycle.go):
//
//	server_name     — activity.tool_call.{started,completed,rejected},
//	                  activity.policy_decision, activity.quarantine_change,
//	                  activity.tool_quarantine_change,
//	                  activity.prompt_get.completed, oauth.token_refreshed,
//	                  oauth.refresh_failed, security.scan_settled,
//	                  security.integrity_alert
//	server          — the servers.changed coalescer extras (enable_toggle,
//	                  quarantine_toggle, restart, tools_changed, tools_approved,
//	                  tools_blocked, server_connected, server_disconnected,
//	                  server_state_changed, …)
//	target_server   — activity.internal_tool_call.completed
//	affected_entity — activity.config_change
//
// A key counts only when its value is a NON-EMPTY string, so an event that
// carries no server name at all (config.saved's `path`,
// security.scanner_changed's `scanner_id`) is unaffected, and an empty name is
// not read as "a server called empty-string".
//
// Round 10 corrected what "unaffected" means for a frame that OUGHT to name a
// server and does not — that is undecidable, not harmless, and it is decided by
// identityBearingEventTypes below rather than by this list.
//
// This is a KEY list rather than a per-type table on purpose: a new event type
// that names a server through one of these keys — the shape every producer in
// the tree already uses — is scoped on the day it is added, instead of leaking
// until someone remembers to extend a table. A producer inventing a brand new
// key name is the one remaining gap, and that is what
// TestSSE_EveryRuntimeEventTypeIsClassified pins.
var eventServerIdentityFields = []string{
	"server_name",
	"server",
	"target_server",
	"affected_entity",
}

// adminConfigEventTypes carry no server identity at all, but they announce a
// mutation of the ADMIN CONFIG DOCUMENT: which file was reloaded or saved, and
// which secret was added, changed or deleted.
//
// They are dropped for a scoped caller for parity with the door beside them.
// This change made GET /api/v1/config a 403 for an agent token rather than a
// filtered document, because it is an admin document and not a server list; a
// live notification stream about that same document must not be the way back
// in. Nothing first-party is affected: the Web UI and the tray read this
// stream as admins.
var adminConfigEventTypes = map[internalRuntime.EventType]struct{}{
	internalRuntime.EventTypeConfigReloaded: {},
	internalRuntime.EventTypeConfigSaved:    {},
	internalRuntime.EventTypeSecretsChanged: {},
}

// identityBearingEventTypes are the event types whose payload is ABOUT one
// upstream server. Membership is a property of the TYPE, not of whether the
// producer happened to fill the field in on this particular publish.
//
// #1166 round 10. The key scan below drops a frame only when an identity key
// holds a NON-EMPTY string, so an identity-bearing frame whose name is missing
// sailed through to a scoped subscriber intact. That is not hypothetical:
// Runtime.EmitActivityInternalToolCall OMITS `target_server` entirely when it is
// empty (event_bus.go), which is every built-in call that is not routed to one
// upstream — retrieve_tools, describe_tool, upstream_servers — and those frames
// carry `arguments` and `response`, i.e. the fleet-wide tool inventory the rest
// of this door exists to withhold. EmitActivityPromptGet and the tool-call
// emitters always SET `server_name` and would publish it as "" on the same
// paths.
//
// The safe default for "this frame is about a server and I cannot tell which"
// is to DROP it, for the same reason auth.RevealSecretsAllowed fails closed:
// an unidentifiable payload cannot be checked against a scope, so it cannot be
// shown to a scoped caller. Deciding by type rather than by populated value
// also means a producer that starts omitting a field — the exact regression
// shape above — cannot silently reopen the door.
//
// servers.changed is deliberately absent: it is the coalesced, state-carrying
// event that eventVisibleToCaller never drops and
// renderEventPayloadForCaller narrows instead.
//
// TestSSE_IdentityBearingEventTypesCoverEveryNamingProducer pins this set
// against the fixtures that exercise each type end-to-end, and
// TestSSE_EveryRuntimeEventTypeIsClassified pins those fixtures against the
// runtime's own constants — so a new event type must be classified in BOTH
// places before it ships.
var identityBearingEventTypes = map[internalRuntime.EventType]struct{}{
	internalRuntime.EventTypeActivityConfigChange:         {},
	internalRuntime.EventTypeActivityInternalToolCall:     {},
	internalRuntime.EventTypeActivityToolCallStarted:      {},
	internalRuntime.EventTypeActivityToolCallCompleted:    {},
	internalRuntime.EventTypeActivityToolCallRejected:     {},
	internalRuntime.EventTypeActivityPolicyDecision:       {},
	internalRuntime.EventTypeActivityQuarantineChange:     {},
	internalRuntime.EventTypeActivityToolQuarantineChange: {},
	internalRuntime.EventTypeActivityPromptGet:            {},
	internalRuntime.EventTypeOAuthTokenRefreshed:          {},
	internalRuntime.EventTypeOAuthRefreshFailed:           {},
	internalRuntime.EventTypeSecurityScanSettled:          {},
	internalRuntime.EventTypeSecurityIntegrityAlert:       {},
}

// eventVisibleToCaller reports whether a runtime event may be delivered to the
// SSE subscriber carried by ctx AT ALL.
//
// DROPPING the frame, rather than redacting the identifying field, is the
// deliberate choice:
//
//   - These are audit notifications ABOUT one resource. Blanking the name
//     leaves `{"action":"server_disabled","changed_fields":["enabled"],
//     "previous_values":{"enabled":true}}` on the wire — the mutation, its
//     shape and its timing, which is most of what the disclosure was — and the
//     surviving frame is still an exact count oracle for how many servers the
//     caller cannot see, one frame at a time, live.
//   - No consumer depends on receiving every frame. The Web UI's authoritative
//     mergeServers — the reason renderEventPayloadForCaller renders rather than
//     edits — keys off servers.changed, which is never dropped, and the Web UI
//     authenticates with the admin API key. The Swift tray reads the same
//     stream over the unix socket, which bypasses the API key entirely
//     (OS-level auth) and is never scoped either. A scoped subscriber is an
//     mcp_agt_ token, and every frame dropped here is about a resource it
//     cannot GET, list, or call.
//
// servers.changed is the ONE type never dropped, whatever its extras name. It
// is the state-carrying event and it is COALESCED last-write-wins: a window in
// which both alpha and beta changed publishes ONE event whose marker may name
// beta, so dropping on that name would strand the scoped subscriber's view of
// alpha until some unrelated change happened to fire. It is RENDERED instead —
// renderEventPayloadForCaller narrows the embed and removes the out-of-scope
// extras — which is the narrowing the coalescer's shape actually permits.
//
// An admin, or a request that carries no AuthContext at all, gets every event
// untouched (see auth.CanEnumerateServer for why absence is unrestricted).
func eventVisibleToCaller(ctx context.Context, evt internalRuntime.Event) bool {
	if !auth.IsScopedCaller(ctx) {
		return true
	}
	if evt.Type == internalRuntime.EventTypeServersChanged {
		return true
	}
	if _, adminOnly := adminConfigEventTypes[evt.Type]; adminOnly {
		return false
	}
	named := false
	for _, key := range eventServerIdentityFields {
		name, ok := evt.Payload[key].(string)
		if !ok || name == "" {
			continue
		}
		named = true
		if !canSeeServer(ctx, name) {
			return false
		}
	}
	// The frame is about a server but names none: undecidable, so fail closed.
	// See identityBearingEventTypes for why this is keyed on the TYPE and not
	// on whether the producer happened to populate the field.
	if _, bearsIdentity := identityBearingEventTypes[evt.Type]; bearsIdentity && !named {
		return false
	}
	return true
}

// isOutOfScopeIdentityField reports whether a payload key must be withheld from
// the caller carried by ctx because its value names a server that caller may
// not enumerate.
//
// It applies to the one event type eventVisibleToCaller never drops. Without
// it, a servers.changed whose embed was correctly narrowed to `alpha` still
// shipped `"server":"beta"` beside it in the coalescer extras — and on the
// notify-only path (ListServers failed upstream, so there is no embed to
// narrow) that extra WAS the entire disclosure.
func isOutOfScopeIdentityField(ctx context.Context, key string, value interface{}) bool {
	name, ok := value.(string)
	if !ok || name == "" {
		return false
	}
	for _, identity := range eventServerIdentityFields {
		if key == identity {
			return !canSeeServer(ctx, name)
		}
	}
	return false
}
