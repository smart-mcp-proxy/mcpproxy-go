package server

import (
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Issue #935 — classifying a DISPATCHED tool result for the activity log.
//
// A tool call has two independent failure surfaces:
//
//  1. the call never happened — pre-dispatch validation, quarantine, transport
//     or protocol failure. These return a non-nil error and were always
//     recorded as status="error".
//  2. the call happened and the upstream ANSWERED "this failed" by setting
//     isError:true on the result — a bad argument value, an unknown tool, a
//     server-side validation failure. The MCP round-trip succeeded, so err is
//     nil, and every post-dispatch emit site used to hardcode status="success".
//
// (2) is by far the most common real-world failure, and recording it as a
// success made every consumer of activity status under-report: the tray glance
// error series and its red row markers never fired, and the 24h error counts in
// the usage timeline were correspondingly low (both derive from
// ActivityRecord.Status — see runtime.UsageAggregate.Apply).
//
// Deliberately NOT done here: minting a third status value (e.g.
// "upstream_error") to tell (1) and (2) apart. Every consumer — the usage
// aggregate, the timeline, the tray, the REST filters, the frontend — switches
// on the two known values today, and a new one would be silently dropped by all
// of them. The distinction is left for a follow-up that can migrate the
// consumers together; the error_message already carries the upstream's own
// words, which is what a human debugging the call needs.
//
// This classification affects the ACTIVITY RECORD ONLY. What is returned to the
// caller is untouched: an isError result is still forwarded verbatim, because
// the client is entitled to the upstream's own answer.

const (
	// activityErrorMessageLimit caps the recorded error text. Activity records
	// are persisted and streamed to the UI, so an upstream that answers with a
	// multi-megabyte error body must not be able to bloat the log.
	activityErrorMessageLimit = 512

	// activityErrorMessageEllipsis marks a message the cap above shortened.
	activityErrorMessageEllipsis = "…"

	// upstreamErrorResultMessage is recorded when the upstream flagged the
	// result as an error but supplied nothing readable to explain it. An empty
	// error_message would render as a blank row in the activity UI.
	upstreamErrorResultMessage = "upstream returned an error result (isError=true)"
)

// activityStatusForResult classifies a successfully dispatched tool result for
// the activity log, returning the (status, error_message) pair to record.
//
// It returns ("success", "") for anything that is not an MCP result flagged
// isError — including nil and non-CallToolResult values, which several call
// paths can produce — and ("error", <upstream text>) otherwise.
func activityStatusForResult(result interface{}) (status, errorMsg string) {
	ctr := asCallToolResult(result)
	if ctr == nil || !ctr.IsError {
		return "success", ""
	}
	return "error", upstreamErrorText(ctr)
}

// asCallToolResult normalises the interface{} that upstream.Manager.CallTool
// returns. Both the pointer and the value form are accepted; anything else
// (string, map, nil) yields nil.
func asCallToolResult(result interface{}) *mcp.CallToolResult {
	switch v := result.(type) {
	case *mcp.CallToolResult:
		return v
	case mcp.CallToolResult:
		return &v
	default:
		return nil
	}
}

// upstreamErrorText extracts a human-readable reason from an isError result:
// its text blocks first (that is where MCP servers put the message), then its
// structured content, then a fixed fallback.
func upstreamErrorText(ctr *mcp.CallToolResult) string {
	parts := make([]string, 0, len(ctr.Content))
	for _, c := range ctr.Content {
		var text string
		switch tc := c.(type) {
		case mcp.TextContent:
			text = tc.Text
		case *mcp.TextContent:
			if tc != nil {
				text = tc.Text
			}
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	msg := strings.Join(parts, "\n")
	if msg == "" && ctr.StructuredContent != nil {
		if encoded, err := json.Marshal(ctr.StructuredContent); err == nil {
			msg = strings.TrimSpace(string(encoded))
		}
	}
	if msg == "" {
		return upstreamErrorResultMessage
	}
	if len(msg) > activityErrorMessageLimit {
		// safeTruncateBytes backs the cut up to a rune boundary, so the stored
		// message is always valid UTF-8.
		msg = msg[:safeTruncateBytes(msg, activityErrorMessageLimit)] + activityErrorMessageEllipsis
	}
	return msg
}
