package management

import (
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// ActivityRetentionOperations is implemented by the runtime (Spec 124 FR-035).
// The management service reads it through a type assertion, so a runtime that
// cannot report leaves the section out.
type ActivityRetentionOperations interface {
	// ActivityRetentionState returns the current shortfall record (nil when
	// the caps hold the whole window) and the work-session retention window.
	ActivityRetentionState() (*storage.RetentionShortfall, time.Duration, error)
}

// retentionSettingsFor names the settings to raise for a shortfall cause.
func retentionSettingsFor(cause string) []string {
	switch cause {
	case storage.ShortfallAgeCap:
		return []string{"activity_retention_days"}
	case storage.ShortfallRecordCap:
		return []string{"activity_max_records"}
	case storage.ShortfallSizeCap:
		return []string{"activity_max_size_mb"}
	default:
		return nil
	}
}

// BuildActivityRetentionReport computes the section.
func BuildActivityRetentionReport(shortfall *storage.RetentionShortfall, window time.Duration) *contracts.ActivityRetentionReport {
	rep := &contracts.ActivityRetentionReport{WindowDays: int(window / (24 * time.Hour))}
	if shortfall == nil {
		return rep
	}
	rep.Shortfall = true
	rep.Cause = shortfall.Cause
	observed := shortfall.ObservedAt
	rep.ObservedAt = &observed
	if !shortfall.OldestCompleteSessionAt.IsZero() {
		oldest := shortfall.OldestCompleteSessionAt
		rep.OldestCompleteSessionAt = &oldest
	}
	rep.Raise = retentionSettingsFor(shortfall.Cause)
	return rep
}

// activityRetentionWarningLine is the one runtime warning of a shortfall.
func activityRetentionWarningLine(rep *contracts.ActivityRetentionReport) string {
	line := fmt.Sprintf("The activity log cannot hold %d days of work sessions: a session inside that window lost rows (%s).", rep.WindowDays, rep.Cause)
	if len(rep.Raise) > 0 {
		line += " Raise " + rep.Raise[0] + " to keep the whole window."
	}
	return line
}
