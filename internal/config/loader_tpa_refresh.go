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
	// neutral is the replacement value text the typed decode accepts as
	// "unset" (the key keeps its default). Splicing it over a malformed or
	// duplicated occurrence repairs the document without re-marshalling it.
	neutral string
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
		// Duration rejects null and "", but "0s" decodes to zero, which
		// EffectiveTPARefreshInterval and Validate treat as unset.
		neutral: `"0s"`,
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
		neutral: `false`,
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
		neutral: `null`,
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
		neutral: `null`,
	},
}

// tpaRefreshOccurrence is one refresh-key member found inside a top-level
// `security` object: the canonical key and the byte span of its value.
type tpaRefreshOccurrence struct {
	key        string
	start, end int
	valid      bool
}

// normalizeTPARefreshInterval makes a malformed Spec 101 US3 refresh key in the
// on-disk config (`security.tpa_refresh_interval`, `tpa_fetch_enabled`,
// `tpa_fetch_url`, `tpa_stale_after_days`) unable to stop the daemon booting
// (B1). The name is historical; it covers all four keys.
//
// The typed decode in loadConfigFile rejects e.g. "24hours" or "30" before
// Validate runs, so validation-time tolerance (tpaRefreshErrors is write-time
// only) cannot help. The raw bytes are scanned, never decoded into a map and
// never re-marshalled, so repeated or case-variant `security` objects, key
// order and every unrelated byte keep exactly what the typed decoder alone
// would see. Each refresh key whose value the typed decode would reject, or
// that is repeated, has every occurrence's value replaced in place by a
// neutral value the typed decode reads as unset; every `tpa_fetch_enabled`
// value is replaced by false (the refresh configuration is untrustworthy, so
// the network leg stays off); and one value-free LoadDiagnostic per bad key is
// returned. Any other document is returned byte-for-byte; a document this
// pass cannot scan is also returned untouched so the typed decode reports the
// real parse error.
//
// Only the file-load path calls this. REST/MCP config writes decode with the
// plain decoders and stay strict.
func normalizeTPARefreshInterval(data []byte) ([]byte, []LoadDiagnostic) {
	occs, ok := scanTPARefreshOccurrences(data)
	if !ok {
		return data, nil
	}

	count := map[string]int{}
	badCanon := map[string]bool{}
	for _, o := range occs {
		count[o.key]++
		if count[o.key] > 1 || !o.valid {
			badCanon[o.key] = true
		}
	}

	var diags []LoadDiagnostic
	neutral := map[string]string{}
	for _, c := range tpaRefreshKeyCheckers {
		neutral[c.key] = c.neutral
		if badCanon[c.key] {
			diags = append(diags, LoadDiagnostic{Key: "security." + c.key, Message: c.message})
		}
	}
	if len(diags) == 0 {
		return data, nil
	}

	var out bytes.Buffer
	out.Grow(len(data))
	last := 0
	for _, o := range occs { // document order
		if !badCanon[o.key] && o.key != tpaFetchEnabledKey {
			continue
		}
		out.Write(data[last:o.start])
		out.WriteString(neutral[o.key])
		last = o.end
	}
	out.Write(data[last:])
	return out.Bytes(), diags
}

// scanTPARefreshOccurrences walks every top-level `security` object (matched
// case-insensitively, as the typed decoder does) and returns each refresh-key
// member in document order with the byte span of its value. It never retains or
// reports values. ok is false when the document cannot be scanned (the typed
// decode then reports the real error).
func scanTPARefreshOccurrences(data []byte) (occs []tpaRefreshOccurrence, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false
		}
		if !strings.EqualFold(key, "security") {
			continue
		}
		base := int(dec.InputOffset()) - len(raw)
		if base < 0 || !bytes.Equal(data[base:base+len(raw)], raw) {
			return nil, false
		}
		if !bytes.HasPrefix(raw, []byte("{")) {
			continue
		}
		sd := json.NewDecoder(bytes.NewReader(raw))
		sd.UseNumber()
		if _, err := sd.Token(); err != nil {
			return nil, false
		}
		for sd.More() {
			ft, err := sd.Token()
			if err != nil {
				return nil, false
			}
			field, _ := ft.(string)
			var vraw json.RawMessage
			if err := sd.Decode(&vraw); err != nil {
				return nil, false
			}
			end := int(sd.InputOffset())
			start := end - len(vraw)
			if start < 0 || !bytes.Equal(raw[start:end], vraw) {
				return nil, false
			}
			for _, c := range tpaRefreshKeyCheckers {
				if !strings.EqualFold(field, c.key) {
					continue
				}
				vd := json.NewDecoder(bytes.NewReader(vraw))
				vd.UseNumber()
				var v any
				if err := vd.Decode(&v); err != nil {
					return nil, false
				}
				occs = append(occs, tpaRefreshOccurrence{
					key: c.key, start: base + start, end: base + end, valid: c.valid(v),
				})
			}
		}
	}
	return occs, true
}
