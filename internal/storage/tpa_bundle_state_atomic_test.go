package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

// FR-011a / SC-010: the state and the history row of the activation that
// produced it are written in ONE bbolt transaction. The marshal-failure tests
// fail before the transaction opens, so they pass however the writes are
// grouped; this one makes the history append fail INSIDE the transaction, after
// the state Put already ran, and requires the state to be rolled back with it.
func TestTPABundleState_HistoryFailureInsideTheTransactionRollsBackTheState(t *testing.T) {
	m := newBoundsManager(t)

	prev := tpaFullState()
	require.NoError(t, m.CommitTPABundleState(prev, nil))

	// A plain value under the "history" key makes CreateBucketIfNotExists fail
	// with ErrIncompatibleValue, inside Update, after the state was written.
	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(TPABundleStateBucket)).Put([]byte(tpaBundleHistoryBucket), []byte("not a bucket"))
	}))

	next := tpaFullState()
	next.Active.Sequence = 99
	next.Authorities["publisher"].Watermark = 99
	err := m.CommitTPABundleState(next, tpaEntry(1))
	require.Error(t, err)
	assert.ErrorIs(t, err, berrors.ErrIncompatibleValue)

	got, loadErr := m.LoadTPABundleState()
	require.NoError(t, loadErr)
	assert.Equal(t, prev, got, "a failed history append must roll the state write back in the same transaction")
}
