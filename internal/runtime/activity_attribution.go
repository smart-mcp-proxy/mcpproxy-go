package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// attributionPayloadKey is the event-payload key that carries an
// ActivityAttribution. It is a NESTED object on purpose: the legacy flat
// payload key "profile" (the /mcp/p/<slug> URL slug, which lands in
// metadata.profile) already exists with a different meaning.
const attributionPayloadKey = "attribution"

// ActivityAttribution is the Spec 108 FR-029 scope attribution of one call: the
// profile, client and token IN EFFECT when it ran. It is computed once at emit
// time by the dispatch path (never re-resolved later, so a reassignment does
// not rewrite history), travels in the event payload under "attribution", and
// the activity service copies it onto the first-class record fields.
//
// The zero value means "no attribution" (system events, limiter sheds, callers
// with no MCP/REST request context).
type ActivityAttribution struct {
	Profile       string
	ProfileSource string
	ClientID      string
	// ClientName is the self-reported clientInfo.name: advisory, never
	// authoritative (a client can claim any name).
	ClientName string
	// ClientVersion is the self-reported clientInfo.version that travelled
	// with ClientName when the identity was read off the REQUEST rather than
	// a session (Spec 058 US3: a 2026-07-28 request never ran initialize, so
	// the session store cannot supply it). Empty when the session resolver
	// already stamped metadata.client_version.
	ClientVersion string
	TokenName     string
	// TokenPrefix is the 12-char display prefix of the calling token. It is
	// INTERNAL: persisted on the storage record as the ownership proof (so a
	// scoped caller recognises its own policy_decision and prompt_get rows),
	// never in any API shape, export or SSE frame for another caller.
	TokenPrefix string
	// GrantID is the browser-login grant that authenticated the request (Spec
	// 132): it lands as metadata.grant_id so two logins of one client can be
	// told apart. Not a secret.
	GrantID string

	// WorkSessionID is the work session of the request (Spec 124 D1). It is
	// resolved at the emit site because only the request knows the credential,
	// the client of a 2026-07-28 request and the run name. Empty for emitters
	// that have no request; the activity service then falls back to the
	// connection's cached id.
	WorkSessionID string
	// UserID is the owning user of the calling credential (server edition).
	// INTERNAL like TokenPrefix: it decides who may read the row.
	UserID string
	// RunName is the validated X-MCPProxy-Run label of the request.
	RunName string
	// ProjectName is the basename of the client's workspace root, when known.
	ProjectName string
	// Surface is where a request with no MCP connection came from: the REST
	// client surface (webui, tray, cli, rest). Empty for an MCP connection.
	Surface string

	// Companion marks a blocking policy decision emitted next to a tool_call
	// row of the same attempt (Spec 124 D2); it is not counted again.
	Companion bool
	// PolicyRefusal marks a call MCPProxy itself refused whose row has no
	// error class of its own (builtin management refusals). The row is
	// classified proxy_policy.
	PolicyRefusal bool
}

// IsZero reports whether no field is set.
func (a ActivityAttribution) IsZero() bool { return a == ActivityAttribution{} }

// payload renders the attribution as the nested event-payload object, each key
// only when non-empty, or nil for the zero value so an unattributed event keeps
// exactly the payload it had before Spec 108.
func (a ActivityAttribution) payload() map[string]any {
	if a.IsZero() {
		return nil
	}
	out := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	set("profile", a.Profile)
	set("profile_source", a.ProfileSource)
	set("client_id", a.ClientID)
	set("client_name", a.ClientName)
	set("client_version", a.ClientVersion)
	set("token_name", a.TokenName)
	set("_token_prefix", a.TokenPrefix)
	set("grant_id", a.GrantID)
	set("_work_session_id", a.WorkSessionID)
	set("_user_id", a.UserID)
	set("run_name", a.RunName)
	set("project_name", a.ProjectName)
	set("_surface", a.Surface)
	if a.Companion {
		out["_companion"] = true
	}
	if a.PolicyRefusal {
		out["_policy_refusal"] = true
	}
	return out
}

// attributionFromPayload reads the nested attribution back out of an event
// payload. Absent or malformed input yields the zero value.
func attributionFromPayload(payload map[string]any) ActivityAttribution {
	m := getMapPayload(payload, attributionPayloadKey)
	if m == nil {
		return ActivityAttribution{}
	}
	return ActivityAttribution{
		Profile:       getStringPayload(m, "profile"),
		ProfileSource: getStringPayload(m, "profile_source"),
		ClientID:      getStringPayload(m, "client_id"),
		ClientName:    getStringPayload(m, "client_name"),
		ClientVersion: getStringPayload(m, "client_version"),
		TokenName:     getStringPayload(m, "token_name"),
		TokenPrefix:   getStringPayload(m, "_token_prefix"),
		GrantID:       getStringPayload(m, "grant_id"),
		WorkSessionID: getStringPayload(m, "_work_session_id"),
		UserID:        getStringPayload(m, "_user_id"),
		RunName:       getStringPayload(m, "run_name"),
		ProjectName:   getStringPayload(m, "project_name"),
		Surface:       getStringPayload(m, "_surface"),
		Companion:     m["_companion"] == true,
		PolicyRefusal: m["_policy_refusal"] == true,
	}
}

// applyAttribution copies an attribution onto the first-class record fields.
// ClientName falls back to the session-resolved metadata.client_name that
// withClientInfo already wrote, so the two never disagree on a record. A
// request-carried ClientVersion lands in metadata.client_version — the key
// withClientInfo uses — when the session resolver wrote none, so a modern
// client's record carries the same shape a legacy client's does.
func applyAttribution(rec *storage.ActivityRecord, a ActivityAttribution) {
	rec.Profile = a.Profile
	rec.ProfileSource = a.ProfileSource
	rec.ClientID = a.ClientID
	rec.TokenName = a.TokenName
	rec.ClientName = a.ClientName
	rec.TokenPrefix = a.TokenPrefix
	// Spec 124: the request-resolved work session wins over the connection's
	// cached one, which the builder set before this call.
	if a.WorkSessionID != "" {
		rec.WorkSessionID = a.WorkSessionID
	}
	if rec.UserID == "" {
		rec.UserID = a.UserID
	}
	rec.RunName = a.RunName
	rec.ProjectName = a.ProjectName
	rec.Surface = a.Surface
	rec.Companion = a.Companion && rec.Type == storage.ActivityTypePolicyDecision
	if a.PolicyRefusal && rec.ErrorClass == "" && rec.Status != storage.ActivityStatusSuccess {
		rec.ErrorClass = storage.ErrorClassProxyPolicy
	}
	if rec.ClientName == "" {
		if name, ok := rec.Metadata["client_name"].(string); ok {
			rec.ClientName = name
		}
	}
	// A request-carried identity (Spec 058 US3) also lands under the metadata
	// keys withClientInfo writes for a session-resolved one, because readers
	// such as UsageAggregate.applyClientCalls key per-client usage on
	// metadata.client_name, not on the first-class field.
	setMetadataIfAbsent := func(key, value string) {
		if value == "" {
			return
		}
		if _, ok := rec.Metadata[key]; ok {
			return
		}
		if rec.Metadata == nil {
			rec.Metadata = make(map[string]interface{})
		}
		rec.Metadata[key] = value
	}
	setMetadataIfAbsent("client_name", a.ClientName)
	setMetadataIfAbsent("client_version", a.ClientVersion)
	setMetadataIfAbsent("grant_id", a.GrantID)
	// A browser login (Spec 132) is labelled as such: a row with a grant id was
	// authenticated by an OAuth access token, never by an agent token.
	if a.GrantID != "" {
		setMetadataIfAbsent("credential_kind", string(auth.CredentialKindOAuth))
	}
}

// NewUsageAggregate returns an empty aggregate at the current admission
// version, for callers that fold a filtered stream of records through Apply
// (GET /activity/usage under a profile/client/token filter, Spec 108 FR-031)
// instead of reading the persisted lifetime rollup.
func NewUsageAggregate() *UsageAggregate { return newUsageAggregate() }
