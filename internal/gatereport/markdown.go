package gatereport

import (
	"fmt"
	"strings"
	"time"
)

// statusEmoji renders a status for the human summary table.
func statusEmoji(s Status) string {
	switch s {
	case StatusPass:
		return "✅ pass"
	case StatusFlaky:
		return "🟡 flaky"
	case StatusFail:
		return "❌ fail"
	case StatusSkipped:
		return "⏭️ skipped"
	case StatusNotRun:
		return "⚪ not-run"
	case StatusAdvisoryFail:
		return "🟠 advisory-fail"
	default:
		return string(s)
	}
}

// Markdown renders the human summary, suitable for GITHUB_STEP_SUMMARY.
func Markdown(r *Report) string {
	var b strings.Builder
	verdict := "❌ FAIL"
	if r.Passed() {
		verdict = "✅ PASS"
	}
	fmt.Fprintf(&b, "# Release QA Gate — %s\n\n", verdict)
	fmt.Fprintf(&b, "Generated: %s\n\n", r.GeneratedAt.Format(time.RFC3339))

	b.WriteString("| Entry | Status | Blocking | Duration | Retries | Reason |\n")
	b.WriteString("|---|---|---|---:|---:|---|\n")
	for i := range r.Entries {
		e := &r.Entries[i]
		blocking := "no"
		if e.Blocking {
			blocking = "yes"
		}
		name := e.Name
		if !e.Expected {
			name += " (unexpected)"
		}
		dur := ""
		if e.DurationMS > 0 {
			dur = (time.Duration(e.DurationMS) * time.Millisecond).Round(time.Millisecond).String()
		}
		reason := e.Reason
		if e.Classification != "" {
			reason = fmt.Sprintf("[%s] %s", e.Classification, reason)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s |\n",
			name, statusEmoji(e.Status), blocking, dur, e.Retries, mdEscape(reason))
	}

	if len(r.BlockingFailures) > 0 {
		b.WriteString("\n## Blocking failures\n\n")
		for _, f := range r.BlockingFailures {
			fmt.Fprintf(&b, "- %s\n", mdEscape(f))
		}
	}
	if len(r.AdvisoryFailures) > 0 {
		b.WriteString("\n## Advisory failures (non-blocking)\n\n")
		for _, f := range r.AdvisoryFailures {
			fmt.Fprintf(&b, "- %s\n", mdEscape(f))
		}
	}
	return b.String()
}

// mdEscape keeps free-form reasons from breaking the summary table.
func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
