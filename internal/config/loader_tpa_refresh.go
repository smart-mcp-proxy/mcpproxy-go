package config

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// tpaRefreshIntervalKey is the JSON key whose diagnostic text is historical;
// the other refresh keys are listed in tpaRefreshKeyCheckers.
const tpaRefreshIntervalKey = "tpa_refresh_interval"

// tpaFetchEnabledKey is forced false when any refresh key is malformed.
const tpaFetchEnabledKey = "tpa_fetch_enabled"

// tpaRefreshIntervalMalformedMessage is the operator-facing diagnostic for the
// interval. It never echoes the offending value (operator-typed free text).
const tpaRefreshIntervalMalformedMessage = "security.tpa_refresh_interval is not a valid duration (use a string such as \"24h\"); ignoring it, using the 24h default, and treating the TPA bundle network fetch as off"

// tpaRefreshKeyCheck describes one refresh key: how to recognise a value the
// typed decode would reject and the value-free diagnostic to emit.
type tpaRefreshKeyCheck struct {
	key     string
	valid   func(v any) bool
	message string
}

// tpaRefreshKeyCheckers mirrors the typed decode of the four Spec 101 US3
// refresh fields. null is accepted for the three non-Duration keys (the typed
// decode treats it as a no-op); the shared Duration decoder rejects it.
var tpaRefreshKeyCheckers = []tpaRefreshKeyCheck{
	{
		key: tpaRefreshIntervalKey,
		valid: func(v any) bool {
			s, ok := v.(string)
			if !ok {
				return false
			}
			_, err := time.ParseDuration(s)
			return err == nil
		},
		message: tpaRefreshIntervalMalformedMessage,
	},
	{
		key: tpaFetchEnabledKey,
		valid: func(v any) bool {
			if v == nil {
				return true
			}
			_, ok := v.(bool)
			return ok
		},
		message: "security.tpa_fetch_enabled is not a boolean (use true or false); ignoring it and treating the TPA bundle network fetch as off",
	},
	{
		key: "tpa_fetch_url",
		valid: func(v any) bool {
			if v == nil {
				return true
			}
			_, ok := v.(string)
			return ok
		},
		message: "security.tpa_fetch_url is not a string; ignoring it and treating the TPA bundle network fetch as off",
	},
	{
		key: "tpa_stale_after_days",
		valid: func(v any) bool {
			if v == nil {
				return true
			}
			n, ok := v.(json.Number)
			if !ok {
				return false
			}
			_, err := strconv.ParseInt(n.String(), 10, 64)
			return err == nil
		},
		message: "security.tpa_stale_after_days is not a whole number of days; ignoring it, using the 30-day default, and treating the TPA bundle network fetch as off",
	},
}

// normalizeTPARefreshInterval makes a malformed Spec 101 US3 refresh key in the
// on-disk config (`security.tpa_refresh_interval`, `tpa_fetch_enabled`,
// `tpa_fetch_url`, `tpa_stale_after_days`) unable to stop the daemon booting
// (B1). The name is historical; it covers all four keys.
//
// The typed decode in loadConfigFile rejects e.g. "24hours" or "30" before
// Validate runs, so validation-time tolerance (tpaRefreshErrors is write-time
// only) cannot help. Each present key whose value the typed decode would reject
// is dropped from the document, `security.tpa_fetch_enabled` is forced false
// (the refresh configuration is untrustworthy, so the network leg stays off),
// and one value-free LoadDiagnostic per bad key is returned. Any other document
// is returned byte-for-byte; a document this pass cannot decode is also returned
// untouched so the typed decode reports the real parse error.
//
// Only the file-load path calls this. REST/MCP config writes decode with the
// plain decoders and stay strict.
func normalizeTPARefreshInterval(data []byte) ([]byte, []LoadDiagnostic) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return data, nil
	}

	var diags []LoadDiagnostic
	seen := map[string]bool{}
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
			for _, c := range tpaRefreshKeyCheckers {
				if strings.EqualFold(k, c.key) && !c.valid(v) {
					badKeys = append(badKeys, k)
					if !seen[c.key] {
						seen[c.key] = true
						diags = append(diags, LoadDiagnostic{Key: "security." + c.key, Message: c.message})
					}
				}
			}
		}
		if len(badKeys) == 0 {
			continue
		}
		for _, k := range badKeys {
			delete(sec, k)
		}
		for k := range sec {
			if strings.EqualFold(k, tpaFetchEnabledKey) {
				delete(sec, k)
			}
		}
		sec[tpaFetchEnabledKey] = false
	}
	if len(diags) == 0 {
		return data, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return data, nil
	}
	// Deterministic order for the diagnostics (map iteration above).
	order := map[string]int{}
	for i, c := range tpaRefreshKeyCheckers {
		order["security."+c.key] = i
	}
	for i := 1; i < len(diags); i++ {
		for j := i; j > 0 && order[diags[j].Key] < order[diags[j-1].Key]; j-- {
			diags[j], diags[j-1] = diags[j-1], diags[j]
		}
	}
	return out, diags
}
