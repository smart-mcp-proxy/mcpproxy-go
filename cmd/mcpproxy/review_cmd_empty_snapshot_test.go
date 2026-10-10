package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// approveBody returns the decoded body of the write to path.
func approveBody(t *testing.T, recorder *reviewRecorder, path string) map[string]any {
	t.Helper()
	for _, req := range recorder.writes() {
		if req.path == path {
			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(req.body), &body))
			return body
		}
	}
	t.Fatalf("no write to %s in %#v", path, recorder.requests)
	return nil
}

// UX-02 cross-review r7 finding 2: an approval of an EMPTY review is bound to
// the empty snapshot, so a tool the core captures between the review read and
// the approval write makes the approval fail as out of date (409) instead of
// being approved unseen. Web and tray already send {} for an empty review.
func TestReviewApproveEmptyReviewBindsEmptySnapshot(t *testing.T) {
	t.Run("quarantined, confirmed at the prompt", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		var prompt string
		_, err := runReviewApprove(t, "table", func(message string) (bool, error) {
			prompt = message
			return true, nil
		}, "bare", "--force")
		require.NoError(t, err)
		require.Contains(t, prompt, "without seeing tools")
		body := approveBody(t, recorder, "/api/v1/servers/bare/security/approve")
		require.Contains(t, body, "expected_hashes", "an empty review must not send an unbound approval")
		require.Equal(t, map[string]any{}, body["expected_hashes"])
		require.Equal(t, true, body["force"])
	})
	t.Run("quarantined, --yes", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "bare", "--yes")
		require.NoError(t, err)
		body := approveBody(t, recorder, "/api/v1/servers/bare/security/approve")
		require.Equal(t, map[string]any{}, body["expected_hashes"])
	})
	t.Run("trusted approve_all", func(t *testing.T) {
		recorder := &reviewRecorder{}
		newMemoryReviewDaemon(t, recorder)
		_, err := runReviewApprove(t, "table", nil, "trusted", "--yes")
		require.NoError(t, err)
		body := approveBody(t, recorder, "/api/v1/servers/trusted/tools/approve")
		require.Equal(t, true, body["approve_all"])
		require.Equal(t, map[string]any{}, body["expected_hashes"])
	})
}
