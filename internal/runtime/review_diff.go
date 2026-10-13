package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func buildReviewDiff(previous ReviewToolPrevious, current ReviewTool) *ReviewToolDiff {
	diff := &ReviewToolDiff{
		Description:  reviewUnifiedDiff(previous.Description, current.Description),
		InputSchema:  reviewUnifiedDiff(formatReviewSchema(previous.InputSchema), formatReviewSchema(current.InputSchema)),
		OutputSchema: reviewUnifiedDiff(formatReviewSchema(previous.OutputSchema), formatReviewSchema(current.OutputSchema)),
	}
	oldAnnotations, _ := json.Marshal(previous.Annotations)
	newAnnotations, _ := json.Marshal(current.Annotations)
	diff.Annotations = reviewUnifiedDiff(string(oldAnnotations), string(newAnnotations))
	if diff.Description == "" && diff.InputSchema == "" && diff.OutputSchema == "" && diff.Annotations == "" {
		return nil
	}
	return diff
}

func formatReviewSchema(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		return string(raw)
	}
	return formatted.String()
}

// reviewUnifiedDiff emits a single unified hunk. Reviews compare complete
// tool contracts, so omitting context avoids duplicating potentially large
// unchanged schemas while preserving exact before/after evidence.
func reviewUnifiedDiff(before, after string) string {
	if before == after {
		return ""
	}
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")
	var b strings.Builder
	oldRange, newRange := "1", "1"
	if len(oldLines) != 1 {
		oldRange = fmt.Sprintf("1,%d", len(oldLines))
	}
	if len(newLines) != 1 {
		newRange = fmt.Sprintf("1,%d", len(newLines))
	}
	fmt.Fprintf(&b, "@@ -%s +%s @@\n", oldRange, newRange)
	for _, line := range oldLines {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range newLines {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}
