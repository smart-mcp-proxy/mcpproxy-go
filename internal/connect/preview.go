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

	cfgPath := ConfigPath(clientID, s.homeDir)
	if cfgPath == "" {
		return nil, fmt.Errorf("cannot determine config path for %s", clientID)
	}

	// Determine create-vs-overwrite via an on-demand read. This is the same
	// scoped, explicit-action read semantics as GetStatus: only touched when the
	// file exists, so an absent config raises no macOS App-Data prompt.
	accessState := accessAbsent
	entryExists := false
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		raw, rerr := s.read(cfgPath)
		if rerr != nil {
			if classifyAccess(rerr) == accessDenied {
				// A denial must surface the actionable remediation, never a
				// misleading "no changes" preview (Spec 078 FR-012).
				return nil, s.newAccessError(client, cfgPath, rerr)
			}
			accessState = classifyAccess(rerr)
		} else {
			exists, parsedOK := s.previewEntryExists(*client, raw, serverName)
			if parsedOK {
				accessState = accessAccessible
				entryExists = exists
			} else {
				// Unparseable config: the preview cannot claim "create" or
				// "overwrite" honestly; report malformed and let the UI degrade.
				accessState = accessMalformed
			}
		}
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
		Client:         clientID,
		ConfigPath:     cfgPath,
		Format:         client.Format,
		ServerKey:      client.ServerKey,
		ServerName:     serverName,
		Entry:          maskedEntry,
		EntryText:      entryText,
		EntryExists:    entryExists,
		ContainsAPIKey: s.containsCredential(),
		Bridge:         client.Bridge,
		AccessState:    accessState,
	}, nil
}

// previewEntryExists reports whether an entry under the exact serverName the
// write would target already exists in the parsed config, and whether the bytes
// parsed at all (parsedOK=false => malformed). It mirrors the create-vs-overwrite
// decision connectJSON / connectTOML make (`serversMap[serverName]` presence),
// so preview's classification matches the write's force behavior. It never
// touches the filesystem — the caller supplies already-read bytes.
func (s *Service) previewEntryExists(client ClientDef, raw []byte, serverName string) (exists, parsedOK bool) {
	var data map[string]interface{}
	if client.Format == "toml" {
		if _, err := toml.Decode(string(raw), &data); err != nil {
			return false, false
		}
	} else if err := unmarshalLenientJSON(raw, &data); err != nil {
		return false, false
	}

	serversMap, ok := data[client.ServerKey].(map[string]interface{})
	if !ok {
		return false, true
	}
	_, exists = serversMap[serverName]
	return exists, true
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
