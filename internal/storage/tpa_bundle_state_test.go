package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

func tpaBucketExists(t *testing.T, m *Manager) bool {
	t.Helper()
	var exists bool
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		exists = tx.Bucket([]byte(TPABundleStateBucket)) != nil
		return nil
	}))
	return exists
}

func tpaFullState() *scanner.TPABundleState {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	return &scanner.TPABundleState{
		SchemaVersion:    scanner.TPABundleStateSchemaVersion,
		SignatureRatchet: true,
		Active: &scanner.TPABundleActiveRecord{
			Authority:         scanner.AuthorityPublisher,
			TrustEpoch:        2,
			Sequence:          41,
			FullDigest:        "aa11",
			SidecarDigest:     "bb22",
			SignatureVerified: true,
			Source:            "remote",
			Path:              "/data/tpa-lkg/aa11.json",
			GeneratedAt:       "2026-10-19T00:00:00Z",
			ActivatedAt:       at,
			Rollback:          true,
		},
		Authorities: map[string]*scanner.TPAAuthorityState{
			scanner.AuthorityPublisher: {
				Epoch:                 2,
				Watermark:             44,
				WatermarkSetBy:        "cc33",
				ExternalEverActivated: true,
				Pin:                   &scanner.TPABundlePin{Sequence: 41, FullDigest: "aa11", Embedded: false, PinnedAt: at},
				DenyList: []scanner.TPABundleDenyEntry{
					{Sequence: 43, FullDigest: "dd44", DeniedAt: at},
					{Sequence: 44, FullDigest: "ee55", DeniedAt: at.Add(time.Minute)},
				},
			},
			scanner.AuthorityOperator: {Epoch: 0, Watermark: 0},
		},
	}
}

func tpaEntry(seq int) *scanner.TPABundleActivation {
	return &scanner.TPABundleActivation{
		Authority:         scanner.AuthorityPublisher,
		TrustEpoch:        1,
		Sequence:          fmt.Sprintf("%d", seq),
		FullDigest:        fmt.Sprintf("digest-%d", seq),
		ActivatedAt:       time.Date(2026, 10, 20, 12, seq, 0, 0, time.UTC),
		Source:            "remote",
		SignatureVerified: true,
	}
}

func TestTPABundleState_AbsentBucketReadsNilAndIsNotCreated(t *testing.T) {
	m := newBoundsManager(t)

	st, err := m.LoadTPABundleState()
	require.NoError(t, err)
	assert.Nil(t, st)

	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	assert.Empty(t, hist)

	assert.False(t, tpaBucketExists(t, m), "a read must never create the bucket")
}

func TestTPABundleState_RoundTripEveryField(t *testing.T) {
	m := newBoundsManager(t)
	want := tpaFullState()

	require.NoError(t, m.CommitTPABundleState(want, nil))

	got, err := m.LoadTPABundleState()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, got)

	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	assert.Empty(t, hist, "a nil entry appends nothing")
}

func TestTPABundleState_ZeroSchemaVersionIsStamped(t *testing.T) {
	m := newBoundsManager(t)
	require.NoError(t, m.CommitTPABundleState(&scanner.TPABundleState{SignatureRatchet: true}, nil))

	got, err := m.LoadTPABundleState()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, scanner.TPABundleStateSchemaVersion, got.SchemaVersion)
}

func TestTPABundleState_CommitWithEntryAppendsHistory(t *testing.T) {
	m := newBoundsManager(t)

	require.NoError(t, m.CommitTPABundleState(tpaFullState(), tpaEntry(1)))
	require.NoError(t, m.CommitTPABundleState(tpaFullState(), tpaEntry(2)))

	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	require.Len(t, hist, 2)
	assert.Equal(t, *tpaEntry(1), hist[0])
	assert.Equal(t, *tpaEntry(2), hist[1])
}

// SC-010 replay: N activations survive a close and reopen of the same data
// directory, oldest first, field for field.
func TestTPABundleState_HistoryOrderedAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)

	const n = 5
	for i := 1; i <= n; i++ {
		require.NoError(t, m.CommitTPABundleState(tpaFullState(), tpaEntry(i)))
	}
	require.NoError(t, m.Close())

	m, err = NewManager(dir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	// A further append after reopen continues the sequence rather than
	// overwriting an earlier row.
	require.NoError(t, m.CommitTPABundleState(tpaFullState(), tpaEntry(n+1)))

	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	require.Len(t, hist, n+1)
	for i, h := range hist {
		assert.Equal(t, *tpaEntry(i + 1), h, "row %d", i)
	}
}

// FR-011a: state and history row commit atomically. An entry that cannot be
// marshalled (time.Time rejects years outside 0..9999) must leave both the
// previous state and the history untouched.
func TestTPABundleState_FailedEntryMarshalWritesNeither(t *testing.T) {
	m := newBoundsManager(t)

	prev := tpaFullState()
	require.NoError(t, m.CommitTPABundleState(prev, tpaEntry(1)))

	next := tpaFullState()
	next.Active.Sequence = 99
	bad := tpaEntry(2)
	bad.ActivatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	require.Error(t, m.CommitTPABundleState(next, bad))

	got, err := m.LoadTPABundleState()
	require.NoError(t, err)
	assert.Equal(t, prev, got, "state must be unchanged")

	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	assert.Equal(t, []scanner.TPABundleActivation{*tpaEntry(1)}, hist, "history must be unchanged")
}

func TestTPABundleState_FailedFirstCommitCreatesNothing(t *testing.T) {
	m := newBoundsManager(t)
	bad := tpaEntry(1)
	bad.ActivatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)

	require.Error(t, m.CommitTPABundleState(tpaFullState(), bad))

	st, err := m.LoadTPABundleState()
	require.NoError(t, err)
	assert.Nil(t, st)
	assert.False(t, tpaBucketExists(t, m), "marshal runs before the bucket is touched")
}

func TestTPABundleState_CorruptStateReturnsError(t *testing.T) {
	m := newBoundsManager(t)
	require.NoError(t, m.CommitTPABundleState(tpaFullState(), nil))

	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(TPABundleStateBucket)).Put([]byte("state"), []byte("{not json"))
	}))

	st, err := m.LoadTPABundleState()
	require.Error(t, err, "callers fail safe on a corrupt record; never a silent nil")
	assert.Nil(t, st)
}

func TestTPABundleState_CorruptHistoryRowReturnsError(t *testing.T) {
	m := newBoundsManager(t)
	require.NoError(t, m.CommitTPABundleState(tpaFullState(), tpaEntry(1)))

	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		h := tx.Bucket([]byte(TPABundleStateBucket)).Bucket([]byte("history"))
		return h.Put([]byte{0xff, 0, 0, 0, 0, 0, 0, 0}, []byte("garbage"))
	}))

	_, err := m.ListTPABundleHistory()
	require.Error(t, err)
}

// The state is installation-wide and keyed by authority: removing a server
// through the purge path must leave it intact.
func TestTPABundleState_ServerRemovalLeavesBucketIntact(t *testing.T) {
	m := newBoundsManager(t)
	want := tpaFullState()
	require.NoError(t, m.CommitTPABundleState(want, tpaEntry(1)))

	cfg := &config.ServerConfig{Name: "srv", URL: "http://srv", Protocol: "http", Enabled: true}
	require.NoError(t, m.SaveUpstreamServer(cfg))
	_, err := m.RegisterServerIdentity(cfg, "/tmp/mcp_config.json")
	require.NoError(t, err)
	require.NoError(t, m.DeleteUpstreamServer("srv"))

	got, err := m.LoadTPABundleState()
	require.NoError(t, err)
	assert.Equal(t, want, got)
	hist, err := m.ListTPABundleHistory()
	require.NoError(t, err)
	assert.Len(t, hist, 1)
}
