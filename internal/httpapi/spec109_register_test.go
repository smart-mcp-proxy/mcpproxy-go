package httpapi

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-m (FR-092, T148, M9): the contradiction register closed by its
// referenced FR, mechanically. Every row's disposition starts with
//
//	Resolved:  names at least one FR that spec.md defines AND that a task traces
//	           to a test path that resolves (the traceability resolver);
//	108        any FR it names exists in specs/108-profiles-v3/spec.md;
//	Accepted   ("Accepted: ..." or "Accepted for this spec: ...") carries a
//	           reason of at least 20 characters.
//
// A row with an empty or unrecognised disposition fails.

type p109RegisterRow struct {
	ID          string
	Disposition string
}

var p109RegisterIDRe = regexp.MustCompile(`^[WMCRX]\d+$`)

func p109RegisterRows(t testing.TB) []p109RegisterRow {
	t.Helper()
	text := p109ReadRepo(t, p109SpecDir+"/spec.md")
	start := strings.Index(text, "### Contradiction register")
	require.GreaterOrEqual(t, start, 0, "spec.md has no Contradiction register")
	var rows []p109RegisterRow
	for _, line := range strings.Split(text[start:], "\n")[1:] {
		if strings.HasPrefix(line, "### ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 3 {
			continue
		}
		id := strings.TrimSpace(cells[0])
		if !p109RegisterIDRe.MatchString(id) {
			continue
		}
		// The disposition is the last cell (the contradiction text may itself
		// contain "|", as in "tools approve|reject").
		rows = append(rows, p109RegisterRow{ID: id, Disposition: strings.TrimSpace(cells[len(cells)-1])})
	}
	return rows
}

// p109CheckRegisterRow returns "" when the row is closed, else the reason.
func p109CheckRegisterRow(t testing.TB, row p109RegisterRow, frs109 map[string]bool, frs108 map[string]bool, traced func(fr string) bool) string {
	d := row.Disposition
	switch {
	case d == "":
		return "no disposition"
	case strings.HasPrefix(d, "Resolved:"):
		// Only the part before any "108" hand-off names this spec's FRs.
		body := strings.TrimPrefix(d, "Resolved:")
		if idx := strings.Index(body, "108"); idx >= 0 {
			body = body[:idx]
		}
		named := p109ExpandFRs(body)
		if len(named) == 0 {
			return "Resolved: names no FR"
		}
		anyTraced := false
		for _, fr := range named {
			if !frs109[fr] {
				return "names " + fr + ", which spec.md does not define"
			}
			if traced(fr) {
				anyTraced = true
			}
		}
		if !anyTraced {
			return fmt.Sprintf("none of %v is traced by a task with a resolving test path", named)
		}
		return ""
	case strings.HasPrefix(d, "108"):
		// "108 (FR-044, removal). Interim: ..." names Spec 108 FRs in the first
		// parenthesis only.
		seg := d
		if open := strings.Index(d, "("); open >= 0 {
			if cl := strings.Index(d[open:], ")"); cl >= 0 {
				seg = d[open : open+cl]
			} else {
				seg = d[open:]
			}
		} else {
			seg = ""
		}
		for _, fr := range p109ExpandFRs(seg) {
			if !frs108[fr] {
				return "names " + fr + ", which Spec 108 does not define"
			}
		}
		return ""
	case strings.HasPrefix(d, "Accepted"):
		// "Accepted: reason" and "Accepted for this spec: reason".
		reason := d
		if idx := strings.Index(d, ":"); idx >= 0 {
			reason = d[idx+1:]
		}
		if len(strings.TrimSpace(reason)) < 20 {
			return "Accepted needs a reason of at least 20 characters"
		}
		return ""
	}
	return "disposition must start with Resolved:, 108 or Accepted:"
}

func p109FRTracer(t testing.TB) func(string) bool {
	tasks := p109Tasks(t)
	cache := map[string]bool{}
	rows := p109AllTraceRows(t)
	return func(fr string) bool {
		if v, ok := cache[fr]; ok {
			return v
		}
		ok := false
		// A task line citing the FR whose test path resolves ...
		for _, task := range tasks {
			if task.Obsolete {
				continue
			}
			for _, cited := range p109ExpandFRs(task.Line) {
				if cited == fr && p109TaskResolves(t, task) == "" {
					ok = true
				}
			}
		}
		// ... or a finding-traceability row that cites the FR and names a
		// resolving task (the table maps FRs to the tasks that test them).
		for _, row := range rows {
			for _, cited := range p109ExpandFRs(row.FRs) {
				if cited != fr {
					continue
				}
				for _, tid := range p109ExpandTasks(row.Tests, tasks) {
					if task, exists := tasks[tid]; exists && p109TaskResolves(t, task) == "" {
						ok = true
					}
				}
			}
		}
		cache[fr] = ok
		return ok
	}
}

func TestSpec109RegisterDispositions(t *testing.T) {
	rows := p109RegisterRows(t)
	// W1-W5, M1-M5, C1-C6, R1-R3, X1-X12.
	require.Len(t, rows, 31, "the register lists 31 contradictions")

	frs109, _ := p109SpecFRs(t, p109SpecDir+"/spec.md")
	frs108, _ := p109SpecFRs(t, "specs/108-profiles-v3/spec.md")
	traced := p109FRTracer(t)
	for _, row := range rows {
		if reason := p109CheckRegisterRow(t, row, frs109, frs108, traced); reason != "" {
			assert.Fail(t, "register row not closed", "%s: %s (%q)", row.ID, reason, row.Disposition)
		}
	}
}

// TestSpec109RegisterDispositionsBite is the self-test: an empty disposition,
// an unknown FR, an unneeded Accepted and a Resolved FR no task traces all fail.
func TestSpec109RegisterDispositionsBite(t *testing.T) {
	frs109 := map[string]bool{"FR-001": true, "FR-002": true}
	frs108 := map[string]bool{"FR-044": true}
	traced := func(fr string) bool { return fr == "FR-001" }
	check := func(d string) string {
		return p109CheckRegisterRow(t, p109RegisterRow{ID: "W9", Disposition: d}, frs109, frs108, traced)
	}

	assert.NotEmpty(t, check(""), "an empty disposition must fail")
	assert.NotEmpty(t, check("Resolved: FR-999"), "an FR absent from spec.md must fail")
	assert.NotEmpty(t, check("Resolved: FR-002"), "an FR no task traces must fail")
	assert.NotEmpty(t, check("Resolved:"), "Resolved with no FR must fail")
	assert.NotEmpty(t, check("Accepted: short"), "a short Accepted reason must fail")
	assert.NotEmpty(t, check("108 (FR-999)"), "a 108 FR absent from spec 108 must fail")
	assert.NotEmpty(t, check("Whatever"), "an unknown disposition must fail")

	assert.Empty(t, check("Resolved: FR-001"))
	assert.Empty(t, check("Resolved: FR-001. Binding: 108 (FR-999)"), "the 108 hand-off is not this spec's FR")
	assert.Empty(t, check("108 (FR-044, removal). Interim: FR-057 hides it"))
	assert.Empty(t, check("108"))
	assert.Empty(t, check("Accepted: they are different concepts and renaming breaks scripts"))
}
