package connect

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Undo reverts the connect that produced backupPath, restoring the client
// config to its exact pre-connect state (Spec 078 US3 / FR-008):
//
//   - backupPath != "": the config is restored byte-for-byte from that backup —
//     this is the only revert that can bring back a pre-existing same-named
//     entry a force-connect overwrote (surgical disconnect cannot).
//   - backupPath == "": the preceding connect created the file (no prior file
//     existed, ConnectResult.backup_path was empty); undo deletes the file so
//     the pre-connect "no file" state is restored.
//
// Safety first: Undo refuses (Action "conflict") unless the CURRENT file is
// byte-identical to what that connect produced — i.e. the backup content with
// exactly the mcpproxy entry applied, reconstructed via the same
// buildServerEntry/marshal path the write used. Any other content means the
// user (or another tool) changed the file since the connect, and a restore
// would clobber those edits. Callers should fall back to Disconnect (surgical
// entry removal) in that case. A missing backup refuses with Action
// "not_found". Every mutation takes its own safety backup before touching the
// file, and a permission denial anywhere surfaces as the same typed
// *AccessError as connect/disconnect (403 + remediation at the REST boundary).
//
// Note the drift check also intentionally refuses when the effective listen
// address / API key / require_mcp_auth changed since the connect: the entry the
// service would write today no longer matches the one on disk, so mcpproxy can
// no longer prove the file is untouched.
func (s *Service) Undo(clientID, serverName, backupPath string) (*ConnectResult, error) {
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

	// The backup must be a backup OF THIS client's config: undo must not become
	// an arbitrary-file-restore primitive for API callers.
	if backupPath != "" && !strings.HasPrefix(backupPath, cfgPath+".bak.") {
		return nil, fmt.Errorf("invalid backup path %q: not a backup of %s", backupPath, cfgPath)
	}

	res, err := s.undo(client, cfgPath, serverName, backupPath)
	return res, s.asAccessError(client, cfgPath, err)
}

func (s *Service) undo(client *ClientDef, cfgPath, serverName, backupPath string) (*ConnectResult, error) {
	// Load the pre-connect content (empty when connect created the file).
	var backupRaw []byte
	if backupPath != "" {
		raw, err := s.read(backupPath)
		if os.IsNotExist(err) {
			return &ConnectResult{
				Success:    false,
				Client:     client.ID,
				ConfigPath: cfgPath,
				ServerName: serverName,
				Action:     "not_found",
				Message:    fmt.Sprintf("Backup %s no longer exists; cannot restore. Use disconnect to remove the mcpproxy entry instead.", backupPath),
			}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read backup: %w", err)
		}
		backupRaw = raw
	}

	// Read the CURRENT file. A vanished config means the state already diverged
	// from "just connected" — refuse rather than resurrect files.
	currentRaw, err := s.read(cfgPath)
	if os.IsNotExist(err) {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "conflict",
			Message:    fmt.Sprintf("Config file %s no longer exists; nothing to undo.", cfgPath),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Drift check (FR-008 safety): reconstruct the exact bytes the connect wrote
	// by replaying the same transformation on the backup content. If the current
	// file differs, someone edited it since — refuse instead of clobbering.
	expected, err := s.replayConnectWrite(client, serverName, backupRaw)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(currentRaw, expected) {
		return &ConnectResult{
			Success:    false,
			Client:     client.ID,
			ConfigPath: cfgPath,
			ServerName: serverName,
			Action:     "conflict",
			Message:    fmt.Sprintf("%s changed since mcpproxy connected; refusing to restore the backup over your edits. Use disconnect to remove just the mcpproxy entry.", cfgPath),
		}, nil
	}

	// Safety backup of the current file before any mutation (collision-proof
	// naming guarantees this never destroys the undo backup itself).
	safetyPath, err := backupFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("safety backup failed: %w", err)
	}

	if backupPath == "" {
		// Connect created this file; pre-connect state is "no file".
		if err := os.Remove(cfgPath); err != nil {
			return nil, fmt.Errorf("remove created config: %w", err)
		}
		return &ConnectResult{
			Success:    true,
			Client:     client.ID,
			ConfigPath: cfgPath,
			BackupPath: safetyPath,
			ServerName: serverName,
			Action:     "deleted",
			Message:    fmt.Sprintf("Removed %s — it did not exist before mcpproxy connected (a safety copy was saved to %s)", cfgPath, safetyPath),
		}, nil
	}

	perm := os.FileMode(0o644)
	if info, statErr := os.Stat(cfgPath); statErr == nil {
		perm = info.Mode()
	}
	if err := atomicWriteFile(cfgPath, backupRaw, perm); err != nil {
		return nil, fmt.Errorf("restore from backup: %w", err)
	}

	return &ConnectResult{
		Success:    true,
		Client:     client.ID,
		ConfigPath: cfgPath,
		BackupPath: safetyPath,
		ServerName: serverName,
		Action:     "restored",
		Message:    fmt.Sprintf("Restored %s from backup %s", cfgPath, backupPath),
	}, nil
}

// replayConnectWrite reproduces, from the pre-connect bytes, the exact file
// content the connect wrote: parse (empty map when no prior file), apply the
// same entry the write applies (buildServerEntry with the LIVE unmasked
// params, including OpenCode's adopted-name normalization), and marshal with
// the same encoder. Because Connect itself performed exactly these steps, a
// current file that has not been touched since is byte-identical to this
// reconstruction.
func (s *Service) replayConnectWrite(client *ClientDef, serverName string, backupRaw []byte) ([]byte, error) {
	data := make(map[string]interface{})
	if client.Format == "toml" {
		if len(backupRaw) > 0 {
			if _, err := toml.Decode(string(backupRaw), &data); err != nil {
				return nil, fmt.Errorf("parse backup TOML: %w", err)
			}
		}
		serversMap, _ := data["mcp_servers"].(map[string]interface{})
		if serversMap == nil {
			serversMap = make(map[string]interface{})
		}
		serversMap[serverName] = buildServerEntry(client.ID, s.entryParams(false))
		data["mcp_servers"] = serversMap
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(data); err != nil {
			return nil, fmt.Errorf("encode TOML: %w", err)
		}
		return buf.Bytes(), nil
	}

	if len(backupRaw) > 0 {
		if err := unmarshalLenientJSON(backupRaw, &data); err != nil {
			return nil, fmt.Errorf("parse backup JSON: %w", err)
		}
	}
	serversMap, _ := data[client.ServerKey].(map[string]interface{})
	if serversMap == nil {
		serversMap = make(map[string]interface{})
	}
	// Mirror connectJSON's OpenCode force path: an equivalent entry under a
	// different key was deleted and rewritten under serverName.
	if client.ID == "opencode" {
		if adoptedName, found := findEquivalentJSONServerName(serversMap, s.baseURL(), serverName); found && adoptedName != serverName {
			delete(serversMap, adoptedName)
		}
	}
	serversMap[serverName] = buildServerEntry(client.ID, s.entryParams(false))
	data[client.ServerKey] = serversMap
	return marshalJSONIndent(data)
}
