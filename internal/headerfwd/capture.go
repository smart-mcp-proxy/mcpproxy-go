package headerfwd

import (
	"net/http"
	"sort"
	"strings"
)

// validValue reports whether v is free of the control bytes FR-012 forbids
// (0x00-0x08, 0x0A-0x1F, 0x7F; tab is allowed).
func validValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c <= 0x08 || (c >= 0x0A && c <= 0x1F) || c == 0x7F {
			return false
		}
	}
	return true
}

// connectionListed returns the lowercase names listed in Connection headers.
func connectionListed(h http.Header) map[string]struct{} {
	out := map[string]struct{}{}
	for _, v := range h.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if t := strings.ToLower(strings.TrimSpace(tok)); t != "" {
				out[t] = struct{}{}
			}
		}
	}
	return out
}

// Capture copies from r the headers whose canonical name is in union (the union
// of live allowlists), minus the deny list and any name the request lists in
// Connection. Values are cloned; r.Header is never aliased and the body is never
// read (FR-006, FR-012). Callers must skip Capture when forwarding is disabled.
func Capture(r *http.Request, union map[string]struct{}) Snapshot {
	if r == nil || len(union) == 0 {
		return Snapshot{}
	}
	names := make([]string, 0, len(union))
	for n := range union {
		names = append(names, canon(n))
	}
	sort.Strings(names)
	hop := connectionListed(r.Header)
	out := map[string]string{}
	total := 0
	for _, n := range names {
		if _, dup := out[n]; dup || Denied(n) {
			continue
		}
		if _, listed := hop[strings.ToLower(n)]; listed {
			continue
		}
		vals := r.Header.Values(n)
		if len(vals) == 0 {
			continue
		}
		ok := true
		for _, v := range vals {
			if !validValue(v) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		joined := strings.Join(vals, ", ")
		if joined == "" || len(joined) > MaxValueBytes || total+len(joined) > MaxTotalBytes {
			continue
		}
		total += len(joined)
		out[n] = strings.Clone(joined)
	}
	return newSnapshot(out)
}
