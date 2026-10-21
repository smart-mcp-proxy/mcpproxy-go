package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// tpaRefreshIntervalKey is the JSON key normalized on the file-load path.
const tpaRefreshIntervalKey = "tpa_refresh_interval"

// tpaRefreshIntervalMalformedMessage is the operator-facing diagnostic. It
// never echoes the offending value (it is operator-typed free text).
const tpaRefreshIntervalMalformedMessage = "security.tpa_refresh_interval is not a valid duration (use a string such as \"24h\"); ignoring it, using the 24h default, and treating the TPA bundle network fetch as off"

// normalizeTPARefreshInterval makes a malformed `security.tpa_refresh_interval`
// in the on-disk config unable to stop the daemon booting (Spec 101 US3 B1).
//
// The shared Duration decoder rejects a value such as "24hours" during the typed
// decode in loadConfigFile, before Validate runs, so validation-time tolerance
// (tpaRefreshErrors is write-time only) cannot help. Only this one key is
// touched: when it is present but not a parsable duration string, the key is
// dropped from the document, `security.tpa_fetch_enabled` is forced false (the
// refresh configuration is untrustworthy, so the network leg stays off), and one
// value-free LoadDiagnostic is returned. Any other document is returned
// byte-for-byte; a document this pass cannot decode is also returned untouched
// so the typed decode reports the real parse error.
//
// Only the file-load path calls this. REST/MCP config writes decode with the
// plain Duration decoder and stay strict.
func normalizeTPARefreshInterval(data []byte) ([]byte, []LoadDiagnostic) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return data, nil
	}

	malformed := false
	for rootKey, rootVal := range root {
		if !strings.EqualFold(rootKey, "security") {
			continue
		}
		sec, ok := rootVal.(map[string]any)
		if !ok {
			continue
		}
		var badKeys []string
		for k, v := range sec {
			if !strings.EqualFold(k, tpaRefreshIntervalKey) {
				continue
			}
			if s, isStr := v.(string); isStr {
				if _, err := time.ParseDuration(s); err == nil {
					continue
				}
			}
			badKeys = append(badKeys, k)
		}
		if len(badKeys) == 0 {
			continue
		}
		malformed = true
		for _, k := range badKeys {
			delete(sec, k)
		}
		for k := range sec {
			if strings.EqualFold(k, "tpa_fetch_enabled") {
				delete(sec, k)
			}
		}
		sec["tpa_fetch_enabled"] = false
	}
	if !malformed {
		return data, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return data, nil
	}
	return out, []LoadDiagnostic{{
		Key:     "security." + tpaRefreshIntervalKey,
		Message: tpaRefreshIntervalMalformedMessage,
	}}
}
