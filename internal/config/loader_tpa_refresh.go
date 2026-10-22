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
			d, err := strconv.ParseInt(n.String(), 10, 64)
			return err == nil && d >= 0
		},
		message: "security.tpa_stale_after_days is not a non-negative whole number of days; ignoring it, using the 30-day default, and treating the TPA bundle network fetch as off",
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

	// Map decoding keeps only the last occurrence of a duplicated key, while
	// the typed decode rejects an earlier malformed one. Inspect every
	// occurrence token by token; a duplicated or malformed key is dropped.
	badCanon := scanTPARefreshOccurrences(data)

	var diags []LoadDiagnostic
	for _, c := range tpaRefreshKeyCheckers {
		if badCanon[c.key] {
			diags = append(diags, LoadDiagnostic{Key: "security." + c.key, Message: c.message})
		}
	}
	if len(diags) == 0 {
		return data, nil
	}
	for rootKey, rootVal := range root {
		if !strings.EqualFold(rootKey, "security") {
			continue
		}
		sec, ok := rootVal.(map[string]any)
		if !ok {
			continue
		}
		for k := range sec {
			for _, c := range tpaRefreshKeyCheckers {
				if badCanon[c.key] && strings.EqualFold(k, c.key) {
					delete(sec, k)
				}
			}
		}
		for k := range sec {
			if strings.EqualFold(k, tpaFetchEnabledKey) {
				delete(sec, k)
			}
		}
		sec[tpaFetchEnabledKey] = false
	}

	out, err := json.Marshal(root)
	if err != nil {
		return data, nil
	}
	return out, diags
}

// scanTPARefreshOccurrences walks every `security` object of the document
// token by token and returns the canonical refresh keys that are duplicated
// (case-insensitively) or have at least one malformed occurrence. It never
// retains or reports values. A scan failure yields the partial result: the
// caller already decoded the document, so this only happens on exotic input.
func scanTPARefreshOccurrences(data []byte) map[string]bool {
	bad := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return bad
	}
	count := map[string]int{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return bad
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return bad
		}
		if !strings.EqualFold(key, "security") || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
			continue
		}
		sd := json.NewDecoder(bytes.NewReader(raw))
		sd.UseNumber()
		if _, err := sd.Token(); err != nil {
			return bad
		}
		for sd.More() {
			ft, err := sd.Token()
			if err != nil {
				return bad
			}
			field, _ := ft.(string)
			var v any
			if err := sd.Decode(&v); err != nil {
				return bad
			}
			for _, c := range tpaRefreshKeyCheckers {
				if !strings.EqualFold(field, c.key) {
					continue
				}
				count[c.key]++
				if count[c.key] > 1 || !c.valid(v) {
					bad[c.key] = true
				}
			}
		}
	}
	return bad
}
