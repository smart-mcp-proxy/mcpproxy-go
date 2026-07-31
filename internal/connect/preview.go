package connect

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// apiKeyMask is the placeholder substituted for the real credential in a
// preview, whether it is carried in a header value, a bridge --header arg, or an
// ?apikey= query. It is deliberately human-readable (not percent-encoded) so the
// user plainly sees a credential is written without the secret ever leaving the
// core in a preview payload, log, or telemetry event (Spec 078 FR-004).
const apiKeyMask = "••••" // ••••

// ConnectPreview describes the exact change a subsequent Connect would make to a
// client config, WITHOUT modifying the file or creating a backup (Spec 078 US1).
// The entry is derived from the same buildServerEntry used by the real write, so
// what is previewed equals what is written for the same client and configuration
// (FR-002); the embedded API key is masked for display (FR-004).
type ConnectPreview struct {
	Client         string                 `json:"client"`
	ConfigPath     string                 `json:"config_path"`
	Format         string                 `json:"format"`           // "json" | "toml"
	ServerKey      string                 `json:"server_key"`       // mcpServers / servers / mcp_servers / mcp
	ServerName     string                 `json:"server_name"`      // key written into the config ("mcpproxy")
	Entry          map[string]interface{} `json:"entry"`            // exact entry (masked) that will be written
	EntryText      string                 `json:"entry_text"`       // entry rendered in the client's format (masked)
	EntryExists    bool                   `json:"entry_exists"`     // an entry with this name already exists (overwrite/force case)
	ContainsAPIKey bool                   `json:"contains_api_key"` // the written URL embeds an apikey credential
	Bridge         bool                   `json:"bridge,omitempty"` // connects via a stdio bridge (config created if absent)
	// AccessState classifies the on-demand config read used to determine
	// EntryExists (Spec 075): accessible|absent|malformed. A denied read never
	// reaches here — it is returned as a typed *AccessError (403 + remediation).
	AccessState string `json:"access_state"`

	// ExistingEntrySummary describes the entry this connect would REPLACE, and
	// is populated only when EntryExists (Spec 091 FR-003). It is built by
	// construction from non-secret projections — see EntrySummary — so no
	// arbitrary value from the user's config can ride out in a preview. It is
	// display-only: drift detection uses PreconditionToken, never this.
	ExistingEntrySummary *EntrySummary `json:"existing_entry_summary,omitempty"`

	// PreconditionToken is the opaque, keyed digest of the raw pre-write state
	// this preview describes plus the exact entry that would be written (Spec
	// 091 FR-005). A caller echoes it on the subsequent write; the core
	// recomputes and refuses with a discriminated conflict when anything has
	// drifted — externally (the file or the target entry changed) or
	// proxy-side (credential rotation, auth toggle, address change).
	PreconditionToken string `json:"precondition_token"`

	// ConnectRefusal carries, verbatim, the reason a connect would refuse for
	// this client regardless of user intent — today only a non-create-capable
	// client (OpenCode) whose config is absent. It is produced by the SAME guard
	// the write runs, so the form can hide the Connect control and show the
	// reason instead of letting the user discover it by clicking (Spec 091
	// FR-003). Empty means connectable.
	ConnectRefusal string `json:"connect_refusal,omitempty"`
}

// Preview computes the exact entry a Connect would write for the given client,
// without modifying the config or creating a backup (Spec 078 FR-001). It reads
// the config on demand only to classify create-vs-overwrite (FR-003) and to
// resolve the Spec 075 access state; a permission denial surfaces as the same
// typed *AccessError that connect/disconnect return (FR-012).
func (s *Service) Preview(clientID, serverName string) (*ConnectPreview, error) {
	client := FindClient(clientID)
	if client == nil {
		return nil, fmt.Errorf("unknown client: %s", clientID)
	}
	if !client.Supported {
		return nil, fmt.Errorf("client %s is not supported: %s", client.Name, client.Reason)
	}
	if serverName == "" {
		serverName = defaultServerName
	}

	cfgPath := s.configPath(clientID)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}

	// Determine create-vs-overwrite via an on-demand read. This is the same
	// scoped, explicit-action read semantics as GetStatus: only touched when the
	// file exists, so an absent config raises no macOS App-Data prompt.
	fileExists, existing, accessState, err := s.preWriteState(client, cfgPath, serverName)
	if err != nil {
		// A denial must surface the actionable remediation, never a misleading
		// "no changes" preview (Spec 078 FR-012).
		return nil, err
	}
	var existingSummary *EntrySummary
	if existing != nil {
		// Sanitized projections only — never the entry itself (Spec 091 FR-003).
		existingSummary = buildEntrySummary(existing.name, existing.entry)
	}

	// Build the entry from the SAME constructor the write uses, with the
	// credential masked for display. Because the real write also calls
	// buildServerEntry, the masked entry differs from the written entry only in
	// the credential value — the carrier, shape, and every other field match.
	maskedEntry := buildServerEntry(clientID, s.entryParams(true))

	entryText, err := renderEntrySnippet(client, serverName, maskedEntry)
	if err != nil {
		return nil, fmt.Errorf("render preview entry: %w", err)
	}

	return &ConnectPreview{
		Client:               clientID,
		ConfigPath:           cfgPath,
		Format:               client.Format,
		ServerKey:            client.ServerKey,
		ServerName:           serverName,
		Entry:                maskedEntry,
		EntryText:            entryText,
		EntryExists:          existing != nil,
		ContainsAPIKey:       s.containsCredential(),
		Bridge:               client.Bridge,
		AccessState:          accessState,
		ExistingEntrySummary: existingSummary,
		// The token binds THIS preview to the state it just described, over the
		// unmasked pending entry the write would produce (Spec 091 FR-005).
		PreconditionToken: s.preconditionToken(cfgPath, fileExists, existing,
			buildServerEntry(clientID, s.entryParams(false))),
		// Run the write's own refusal guards so the form learns "not
		// connectable" from the preview, never from a failed click (FR-003).
		// BOTH force-proof guards belong here: the absent-config refusal, and
		// the commented-.jsonc refusal — a commented file parses leniently, so
		// without this the preview would render a clean, enabled Connect for a
		// write that always refuses.
		ConnectRefusal: refusalText(connectRefusal(client, cfgPath), s.guardJsoncComments(cfgPath)),
	}, nil
}

// refusalText renders the first refusal error as its verbatim reason string, or
// empty when every guard passed. Guards are evaluated in the order the write
// runs them, so the reason the user is shown is the one they would have hit.
func refusalText(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

// preWriteState resolves the raw pre-write state shared by the preview and the
// write's precondition check: whether the config file exists, which entry the
// write would actually replace (adoption-aware, nil when none), and the Spec 075
// access classification. Both callers must derive the token from the SAME
// resolution — that is the whole point of the guarantee — so the lookup lives
// here rather than being duplicated.
//
// A permission denial is returned as the typed *AccessError (403 + remediation);
// an unreadable-but-not-denied or unparseable config yields the corresponding
// access state with no resolved entry.
func (s *Service) preWriteState(client *ClientDef, cfgPath, serverName string) (fileExists bool, existing *existingEntry, accessState string, err error) {
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		return false, nil, accessAbsent, nil
	}
	raw, rerr := s.read(cfgPath)
	if rerr != nil {
		state := classifyAccess(rerr)
		if state == accessDenied {
			return true, nil, state, s.newAccessError(client, cfgPath, rerr)
		}
		return true, nil, state, nil
	}
	resolved, parsedOK := s.resolveExistingEntry(*client, raw, serverName)
	if !parsedOK {
		// Unparseable config: the preview cannot claim "create" or "overwrite"
		// honestly; report malformed and let the UI degrade.
		return true, nil, accessMalformed, nil
	}
	return true, resolved, accessAccessible, nil
}

// existingEntry is the entry a write would actually replace: the key it lives
// under (which may differ from the requested server name after adoption), its
// parsed value as an object (nil when the value is not one), and the raw value
// exactly as the config held it.
//
// Both projections are needed: `entry` feeds the sanitized display summary,
// which is defined over object fields, while `value` feeds the precondition
// token, which must see every byte — a config hand-edited to hold a string, a
// number or an array under the target key is still state the user was shown and
// the write would clobber (Spec 091 FR-005).
type existingEntry struct {
	name  string
	entry map[string]interface{}
	value interface{}
}

// resolveExistingEntry returns the entry the write would target in the parsed
// config — nil when there is none — and whether the bytes parsed at all
// (parsedOK=false => malformed). It mirrors the create-vs-overwrite decision
// connectJSON / connectTOML make (`serversMap[serverName]` presence, then
// OpenCode's equivalent-entry adoption), so preview's classification matches the
// write's force behavior, the sanitized summary names the key that actually
// disappears, and the precondition token hashes the value the write would
// clobber. It never touches the filesystem — the caller supplies already-read
// bytes.
func (s *Service) resolveExistingEntry(client ClientDef, raw []byte, serverName string) (found *existingEntry, parsedOK bool) {
	var data map[string]interface{}
	if client.Format == "toml" {
		if _, err := toml.Decode(string(raw), &data); err != nil {
			return nil, false
		}
	} else if err := unmarshalLenientJSON(raw, &data); err != nil {
		return nil, false
	}

	serversMap, ok := data[client.ServerKey].(map[string]interface{})
	if !ok {
		return nil, true
	}
	if value, ok := serversMap[serverName]; ok {
		return newExistingEntry(serverName, value), true
	}
	// OpenCode's write path adopts an already-installed entry pointing at our MCP
	// URL even under a different key (incl. the legacy ?apikey= shape) and
	// normalizes it on connect; mirror that here so preview reports the overwrite
	// instead of a misleading "create" that then diverges from the write (Spec
	// 078 FR-002).
	if client.ID == "opencode" {
		if adopted, found := findEquivalentJSONServerName(serversMap, s.baseURL(), serverName); found {
			return newExistingEntry(adopted, serversMap[adopted]), true
		}
	}
	return nil, true
}

// newExistingEntry wraps a resolved config value; a non-object value yields a
// nil entry map (the key exists, but there is nothing to project) while the raw
// value is kept for the precondition token.
func newExistingEntry(name string, value interface{}) *existingEntry {
	obj, _ := value.(map[string]interface{})
	return &existingEntry{name: name, entry: obj, value: value}
}

// renderEntrySnippet renders the entry as the merge-ready snippet in the
// client's format: for JSON, the server-name-keyed object; for TOML, the
// [mcp_servers.<name>] table. This is what the UI shows in a monospace block as
// the additive change.
func renderEntrySnippet(client *ClientDef, serverName string, entry map[string]interface{}) (string, error) {
	keyed := map[string]interface{}{serverName: entry}
	if client.Format == "toml" {
		nested := map[string]interface{}{client.ServerKey: keyed}
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(nested); err != nil {
			return "", err
		}
		return buf.String(), nil
	}
	encoded, err := marshalJSONIndent(keyed)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
