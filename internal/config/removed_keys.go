//go:build server

package config

import "fmt"

// Spec 107 FR-032/FR-035: keys and auth_broker modes the server edition no
// longer supports. Both the boot normaliser (loader_serveredition.go) and the
// write-time refusal (ValidateRemovedKeys) walk the GENERIC document with the
// same visitor, because json.Unmarshal into the typed Config silently drops an
// unknown key — by the time Config.Validate runs, nothing is left to refuse.
//
// The strings below are the contract (contracts/config-keys.md) and are the
// one place they may legitimately appear in this package: the FR-035 guard
// test forbids them inside the auth_broker validator, not here.

const (
	removedKeyMaxUserServers       = "max_user_servers"
	removedKeyWorkspaceIdleTimeout = "workspace_idle_timeout"
	removedKeyAuthBrokerHeader     = "header"
	removedKeyAuthBrokerHeaderFmt  = "header_format"
	deprecatedKeyStoreIDPTokens    = "store_idp_tokens"
)

// retiredAuthBrokerModes are the never-implemented modes whose whole
// auth_broker block is dropped (FR-032).
var retiredAuthBrokerModes = map[string]struct{}{
	"token_exchange": {},
	"entra_obo":      {},
}

// serverEditionBlockKeys are the top-level keys that carry the server-edition
// block: the canonical one and the legacy alias the loader normalises.
var serverEditionBlockKeys = []string{"server_edition", "teams"}

func removedServerEditionKeyMessage(key string) string {
	return fmt.Sprintf("server_edition.%s is no longer supported and was ignored", key)
}

func removedAuthBrokerModeMessage(mode, serverName string) string {
	return fmt.Sprintf("auth_broker.mode %q was never implemented; the auth_broker block for server %q was ignored", mode, serverName)
}

func removedAuthBrokerLeafMessage(key string) string {
	return fmt.Sprintf("auth_broker.%s is no longer supported and was ignored", key)
}

const deprecatedStoreIDPTokensMessage = "server_edition.store_idp_tokens is deprecated and no longer stores IdP tokens; remove it"

// removedKeyVisitor receives one call per removed key/mode found in a generic
// document. path is the JSON path of the key; message is the contract text.
type removedKeyVisitor func(path, message string)

// walkRemovedKeys visits every removed key/mode in raw, in document order:
// server_edition.{max_user_servers,workspace_idle_timeout}, then for each
// mcpServers[i].auth_broker the mode (when token_exchange/entra_obo) and the
// header/header_format leaves. It never mutates raw.
func walkRemovedKeys(raw map[string]any, visit removedKeyVisitor) {
	if raw == nil {
		return
	}
	for _, blockKey := range serverEditionBlockKeys {
		block, ok := raw[blockKey].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{removedKeyMaxUserServers, removedKeyWorkspaceIdleTimeout} {
			if _, present := block[key]; present {
				visit(blockKey+"."+key, removedServerEditionKeyMessage(key))
			}
		}
	}

	servers, _ := raw["mcpServers"].([]any)
	for i, entry := range servers {
		server, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		broker, ok := server["auth_broker"].(map[string]any)
		if !ok {
			continue
		}
		prefix := fmt.Sprintf("mcpServers[%d].auth_broker", i)
		if mode, _ := broker["mode"].(string); mode != "" {
			if _, removed := retiredAuthBrokerModes[mode]; removed {
				name, _ := server["name"].(string)
				visit(prefix+".mode", removedAuthBrokerModeMessage(mode, name))
			}
		}
		for _, key := range []string{removedKeyAuthBrokerHeader, removedKeyAuthBrokerHeaderFmt} {
			if _, present := broker[key]; present {
				visit(prefix+"."+key, removedAuthBrokerLeafMessage(key))
			}
		}
	}
}

// ValidateRemovedKeys is the write-time refusal for the removed keys/modes
// (FR-039): it walks a generic configuration document — the merged map in
// PATCH /api/v1/config, the decoded body of /config/apply — and returns one
// ValidationError per removed key, with the same message the boot path
// records as a LoadDiagnostic. It must run BEFORE the document is decoded
// into Config, and it never runs on the boot path (boot normalises + records
// instead of refusing). The deprecated store_idp_tokens is not a removed key
// and is never refused. A nil or empty document passes.
func ValidateRemovedKeys(raw map[string]any) []ValidationError {
	var errs []ValidationError
	walkRemovedKeys(raw, func(path, message string) {
		errs = append(errs, ValidationError{Field: path, Message: message})
	})
	return errs
}

// dropRemovedKeys removes every removed key/mode from raw IN PLACE and returns
// one LoadDiagnostic per removal plus the store_idp_tokens deprecation when
// that key is `true`. It is the boot normaliser's core.
func dropRemovedKeys(raw map[string]any) []LoadDiagnostic {
	var diags []LoadDiagnostic
	walkRemovedKeys(raw, func(path, message string) {
		diags = append(diags, LoadDiagnostic{Key: path, Message: message})
	})

	for _, blockKey := range serverEditionBlockKeys {
		block, ok := raw[blockKey].(map[string]any)
		if !ok {
			continue
		}
		delete(block, removedKeyMaxUserServers)
		delete(block, removedKeyWorkspaceIdleTimeout)
		if v, ok := block[deprecatedKeyStoreIDPTokens].(bool); ok && v {
			diags = append(diags, LoadDiagnostic{Key: blockKey + "." + deprecatedKeyStoreIDPTokens, Message: deprecatedStoreIDPTokensMessage})
		}
	}

	servers, _ := raw["mcpServers"].([]any)
	for _, entry := range servers {
		server, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		broker, ok := server["auth_broker"].(map[string]any)
		if !ok {
			continue
		}
		if mode, _ := broker["mode"].(string); mode != "" {
			if _, removed := retiredAuthBrokerModes[mode]; removed {
				delete(server, "auth_broker")
				continue
			}
		}
		delete(broker, removedKeyAuthBrokerHeader)
		delete(broker, removedKeyAuthBrokerHeaderFmt)
	}
	return diags
}
