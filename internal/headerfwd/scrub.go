package headerfwd

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

func jsonEscaped(v string) string {
	b, err := json.Marshal(v)
	if err != nil || len(b) < 2 {
		return v
	}
	return string(b[1 : len(b)-1])
}

// Scrub removes forwarded values from upstream-echoed text (FR-016). It is
// best-effort: transformed echoes (base64, URL-encoding, hashing) are not caught.
// allow lists additional allowlisted names; names present in s are always used.
func Scrub(text string, s Snapshot, allow []string) string {
	if text == "" || s.IsEmpty() {
		return text
	}
	names := s.Names()
	_ = allow // names without a forwarded value have nothing to scrub
	// Name-anchored forms first: they catch values shorter than 4.
	for _, n := range names {
		marker := "[forwarded:" + n + "]"
		for _, v := range uniq(s.h[n], jsonEscaped(s.h[n])) {
			qn, qv := regexp.QuoteMeta(n), regexp.QuoteMeta(v)
			re := regexp.MustCompile(`(?i)((?:"` + qn + `"|` + qn + `)\s*[:=]\s*"?)` + qv)
			text = re.ReplaceAllString(text, "${1}"+strings.ReplaceAll(marker, "$", "$$"))
		}
	}
	// Exact values of length >= 4, longest first.
	type nv struct{ n, v string }
	var vals []nv
	for _, n := range names {
		for _, v := range uniq(s.h[n], jsonEscaped(s.h[n])) {
			if len(s.h[n]) >= 4 && v != "" {
				vals = append(vals, nv{n, v})
			}
		}
	}
	sort.SliceStable(vals, func(i, j int) bool { return len(vals[i].v) > len(vals[j].v) })
	for _, x := range vals {
		text = strings.ReplaceAll(text, x.v, "[forwarded:"+x.n+"]")
	}
	return text
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// minTruncatedPrefix is the shortest cut-off prefix ScrubRecord removes; shorter
// fragments are too common in ordinary text to scrub safely.
const minTruncatedPrefix = 8

// ScrubRecord is Scrub for recording sinks. Response truncation cuts text at a
// raw offset, so a forwarded value straddling the cut survives as a prefix that
// Scrub cannot match; ScrubRecord additionally masks such prefixes (FR-016.3).
func ScrubRecord(text string, s Snapshot) string {
	text = Scrub(text, s, nil)
	if text == "" || s.IsEmpty() {
		return text
	}
	for _, n := range s.Names() {
		for _, v := range uniq(s.h[n], jsonEscaped(s.h[n])) {
			for k := len(v) - 1; k >= minTruncatedPrefix; k-- {
				if strings.Contains(text, v[:k]) {
					text = strings.ReplaceAll(text, v[:k], "[forwarded:"+n+"]")
				}
			}
		}
	}
	return text
}
