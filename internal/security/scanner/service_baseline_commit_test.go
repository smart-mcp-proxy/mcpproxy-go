package scanner

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 117 PR-1 (#1567, T106): every server approval records the server's
// tool baseline inside the runtime's locked commit, before the integrity
// baseline, for both the plain and the with-blocks branch.

type stepStorage struct {
	Storage
	steps *[]string
}

func (s *stepStorage) SaveIntegrityBaseline(b *IntegrityBaseline) error {
	*s.steps = append(*s.steps, "baseline")
	return s.Storage.SaveIntegrityBaseline(b)
}

func (s *stepStorage) SaveIntegrityBaselineWithBlocks(b *IntegrityBaseline, blocks []ToolApprovalBlock) error {
	*s.steps = append(*s.steps, "baseline_and_blocks")
	return s.Storage.SaveIntegrityBaselineWithBlocks(b, blocks)
}

type committingUnquarantiner struct {
	steps   *[]string
	markErr error
	gotBy   string
}

func (u *committingUnquarantiner) UnquarantineServer(string) error {
	*u.steps = append(*u.steps, "unquarantine")
	return nil
}

func (u *committingUnquarantiner) WithToolApprovalLock(serverName string, fn func() error) error {
	*u.steps = append(*u.steps, "legacy-lock:"+serverName)
	return fn()
}

func (u *committingUnquarantiner) CommitServerApproval(serverName string, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	*u.steps = append(*u.steps, "lock:"+serverName)
	defer func() { *u.steps = append(*u.steps, "unlock:"+serverName) }()
	if expected != nil {
		return 0, fmt.Errorf("unexpected binding %v", expected)
	}
	u.gotBy = by
	if u.markErr != nil {
		return 0, u.markErr
	}
	*u.steps = append(*u.steps, "mark")
	return 0, commit()
}

var _ ServerApprovalCommitter = (*committingUnquarantiner)(nil)

func TestServiceApproveServer_MarksBaselineInsideLockedCommit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		blocks []string
		save   string
	}{
		{"plain (empty review)", nil, "baseline"},
		{"with blocks", []string{"delete_issue"}, "baseline_and_blocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _ := newTestService(t)
			steps := []string{}
			svc.storage = &stepStorage{Storage: store, steps: &steps}
			unq := &committingUnquarantiner{steps: &steps}
			svc.SetServerUnquarantiner(unq)

			var err error
			if tc.blocks == nil {
				err = svc.ApproveServer(context.Background(), "qs-server", true, "api")
			} else {
				err = svc.ApproveServerWithBlocks(context.Background(), "qs-server", true, "api", tc.blocks)
			}
			require.NoError(t, err)
			assert.Equal(t, []string{"lock:qs-server", "mark", tc.save, "unlock:qs-server", "unquarantine"}, steps)
			assert.Equal(t, "api", unq.gotBy)
		})
	}
}

func TestServiceApproveServer_MarkFailureCommitsNothing(t *testing.T) {
	svc, store, _ := newTestService(t)
	steps := []string{}
	svc.storage = &stepStorage{Storage: store, steps: &steps}
	svc.SetServerUnquarantiner(&committingUnquarantiner{steps: &steps, markErr: fmt.Errorf("injected")})

	err := svc.ApproveServer(context.Background(), "qs-server", true, "api")
	require.Error(t, err)
	assert.Equal(t, []string{"lock:qs-server", "unlock:qs-server"}, steps, "no baseline and no unquarantine after a failed mark")
	_, getErr := store.GetIntegrityBaseline("qs-server")
	assert.Error(t, getErr, "no integrity baseline saved")
}
