package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-2 (#1566, T209): the scanner's review-bound approval.

type reviewedApprover struct {
	steps        []string
	gotExpected  map[string]string
	gotBlocks    []string
	commitErr    error
	unquarantine int
}

func (u *reviewedApprover) UnquarantineServer(string) error {
	u.unquarantine++
	u.steps = append(u.steps, "unquarantine")
	return nil
}

func (u *reviewedApprover) CommitServerApproval(serverName string, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	u.steps = append(u.steps, "commit-section")
	u.gotExpected = expected
	u.gotBlocks = blocks
	if u.commitErr != nil {
		return 0, u.commitErr
	}
	return 0, commit()
}

func (u *reviewedApprover) FinishServerApproval(string) error {
	u.steps = append(u.steps, "finish")
	return nil
}

var _ ReviewedServerApprover = (*reviewedApprover)(nil)

func TestApproveServerReviewed_PassesBindingThroughOneSection(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := &reviewedApprover{}
	svc.SetServerUnquarantiner(u)

	expected := map[string]string{"read_note": "rv1-abc"}
	require.NoError(t, svc.ApproveServerReviewed(context.Background(), "qs-server", true, "api", []string{"drop_db", "drop_db"}, expected))
	assert.Equal(t, expected, u.gotExpected)
	assert.Equal(t, []string{"drop_db"}, u.gotBlocks, "blocks are deduplicated")
	assert.Equal(t, []string{"commit-section", "finish"}, u.steps, "the reviewed path never calls UnquarantineServer")
	_, err := store.GetIntegrityBaseline("qs-server")
	assert.NoError(t, err)
}

func TestApproveServer_LegacyEntryPointsAreUnbound(t *testing.T) {
	svc, _, _ := newTestService(t)
	u := &reviewedApprover{gotExpected: map[string]string{"sentinel": "x"}}
	svc.SetServerUnquarantiner(u)
	require.NoError(t, svc.ApproveServer(context.Background(), "qs-server", true, "api"))
	assert.Nil(t, u.gotExpected)

	u.gotExpected = map[string]string{"sentinel": "x"}
	require.NoError(t, svc.ApproveServerWithBlocks(context.Background(), "qs-server", true, "api", []string{"t"}))
	assert.Nil(t, u.gotExpected)
}

func TestApproveServerReviewed_StaleErrorPropagatesUnchanged(t *testing.T) {
	svc, store, _ := newTestService(t)
	stale := toolreview.NewStaleToolReviewError("qs-server", []string{"read_note"}, nil, nil)
	u := &reviewedApprover{commitErr: stale}
	svc.SetServerUnquarantiner(u)

	err := svc.ApproveServerReviewed(context.Background(), "qs-server", true, "api", nil, map[string]string{})
	var got *toolreview.StaleToolReviewError
	require.True(t, errors.As(err, &got))
	assert.Same(t, stale, got)
	assert.Equal(t, stale.Error(), err.Error(), "the message is not wrapped")
	assert.Equal(t, []string{"commit-section"}, u.steps, "no finish after a stale binding")
	_, gerr := store.GetIntegrityBaseline("qs-server")
	assert.Error(t, gerr)
}

func TestApproveServerReviewed_DangerousGateRunsFirst(t *testing.T) {
	svc, store, _ := newTestService(t)
	u := &reviewedApprover{}
	svc.SetServerUnquarantiner(u)
	require.NoError(t, store.SaveScanJob(&ScanJob{ID: "job-hard", ServerName: "poisoned", Status: ScanJobStatusCompleted,
		Scanners: []string{"tpa-descriptions"}, StartedAt: time.Now().Add(-time.Minute)}))
	require.NoError(t, store.SaveScanReport(&ScanReport{ID: "report-hard", JobID: "job-hard", ServerName: "poisoned",
		ScannerID: "tpa-descriptions", ScannedAt: time.Now(),
		Findings: []ScanFinding{{RuleID: "phrase.injection", Severity: SeverityHigh, ThreatLevel: ThreatLevelDangerous, Tier: TierHard}}}))

	err := svc.ApproveServerReviewed(context.Background(), "poisoned", false, "api", nil, map[string]string{"x": "rv1-x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dangerous")
	assert.Empty(t, u.steps, "the binding is checked only after the scan gate")
}

// A legacy unquarantiner (no reviewed capability) never drops a binding.
func TestApproveServerReviewed_WithoutCapabilityFailsClosedWhenBound(t *testing.T) {
	svc, store, _ := newTestService(t)
	steps := []string{}
	svc.SetServerUnquarantiner(&committingUnquarantiner{steps: &steps})
	err := svc.ApproveServerReviewed(context.Background(), "qs-server", true, "api", nil, map[string]string{})
	require.Error(t, err)
	assert.Empty(t, steps)
	_, gerr := store.GetIntegrityBaseline("qs-server")
	assert.Error(t, gerr)

	// Unbound keeps the legacy path.
	require.NoError(t, svc.ApproveServerReviewed(context.Background(), "qs-server", true, "api", nil, nil))
	assert.Contains(t, steps, "unquarantine")
}
