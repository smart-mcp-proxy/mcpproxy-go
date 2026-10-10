package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// epochUnquarantiner simulates the runtime: the server was removed and re-added
// after the approval started, so the epoch it hands back no longer matches.
type epochUnquarantiner struct {
	mockUnquarantiner
	epoch   uint64
	current uint64
	ran     bool
}

func (e *epochUnquarantiner) ServerEpoch(string) uint64 { return e.epoch }

func (e *epochUnquarantiner) UnquarantineServerAtEpoch(_ string, epoch uint64, before func() error) error {
	if epoch != e.current {
		return errors.New("server was removed since the approval was started")
	}
	e.ran = true
	return before()
}

// UX-01 r10: an approval started against one incarnation of a server writes no
// baseline and unquarantines nothing for a same-name replacement.
func TestApproveServer_RefusesReplacementIncarnation(t *testing.T) {
	svc, store, _ := newTestService(t)
	unq := &epochUnquarantiner{epoch: 1, current: 2}
	svc.SetServerUnquarantiner(unq)

	err := svc.ApproveServer(context.Background(), "qs-server", true, "admin")
	require.Error(t, err)
	require.False(t, unq.ran)
	_, berr := store.GetIntegrityBaseline("qs-server")
	require.Error(t, berr, "a stale baseline was written for the replacement")

	unq.current = 1
	require.NoError(t, svc.ApproveServer(context.Background(), "qs-server", true, "admin"))
	_, berr = store.GetIntegrityBaseline("qs-server")
	require.NoError(t, berr)
}
