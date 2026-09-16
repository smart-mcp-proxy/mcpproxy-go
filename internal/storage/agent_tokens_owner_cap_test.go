package storage

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 107 FR-037 (issue #1177): auth.MaxTokens is a PER-OWNER cap, not a
// deployment-wide one. Tokens with the same UserID count together; ownerless
// operator tokens (UserID == "", every personal-edition token) form one owner.
//
// Oracle discipline for every test below: a positive control mints the token
// immediately BELOW the cap for the same owner, so the ErrAgentTokenLimitReached
// that follows is the cap and not a name collision or an unwired store.

// fillOwnerToCap mints exactly auth.MaxTokens tokens for one owner and
// requires every one of them to land.
func fillOwnerToCap(t *testing.T, mgr *Manager, owner string) {
	t.Helper()
	for i := 0; i < auth.MaxTokens; i++ {
		token, raw := makeOwnedTestToken(t, fmt.Sprintf("%s-fill-%03d", ownerLabel(owner), i), owner)
		require.NoError(t, mgr.CreateAgentToken(token, raw, testHMACKey),
			"owner %q token %d must land below the cap", owner, i)
	}
}

func ownerLabel(owner string) string {
	if owner == "" {
		return "ownerless"
	}
	return owner
}

// TestAgentTokenCap_PerOwner: owner A at the cap cannot mint a 101st token,
// and owner B — who holds nothing — can still mint their first.
//
// BITES: with the global `tokenBucket.Stats().KeyN >= auth.MaxTokens` count,
// A's 100 tokens fill the bucket and B's first create fails.
func TestAgentTokenCap_PerOwner(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"
	const ownerB = "01HTEST0000000000000USERB"

	fillOwnerToCap(t, mgr, ownerA)

	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	err := mgr.CreateAgentToken(over, raw, testHMACKey)
	require.ErrorIs(t, err, ErrAgentTokenLimitReached, "A's 101st token must hit A's own cap")

	first, raw := makeOwnedTestToken(t, "b-first", ownerB)
	require.NoError(t, mgr.CreateAgentToken(first, raw, testHMACKey),
		"B holds no tokens; A's cap must not block B's first")

	// The bucket now holds MaxTokens+1 rows: the cap is per owner, so the
	// bucket itself is no longer bounded by auth.MaxTokens.
	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	assert.Equal(t, auth.MaxTokens+1, count)
}

// TestAgentTokenCap_OwnerlessTokensAreOneOwner: the operator's ownerless
// tokens count together as ONE owner (so the personal edition is unchanged),
// and they do not count against a user's quota nor a user's against theirs.
//
// BITES: with the global count, 100 ownerless tokens block the user's first.
func TestAgentTokenCap_OwnerlessTokensAreOneOwner(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"

	fillOwnerToCap(t, mgr, "")

	over, raw := makeOwnedTestToken(t, "ownerless-one-too-many", "")
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenLimitReached,
		"the ownerless owner is capped like any other")

	first, raw := makeOwnedTestToken(t, "a-first", ownerA)
	require.NoError(t, mgr.CreateAgentToken(first, raw, testHMACKey),
		"the operator's ownerless tokens must not consume a user's quota")

	// And the other direction: A at the cap does not stop an ownerless mint
	// once a slot is free in the ownerless quota.
	require.NoError(t, mgr.DeleteAgentToken("ownerless-fill-000"))
	freed, raw := makeOwnedTestToken(t, "ownerless-after-delete", "")
	require.NoError(t, mgr.CreateAgentToken(freed, raw, testHMACKey),
		"deleting one of the owner's own tokens must free a slot for that owner")
}

// TestAgentTokenCap_CountsWholeBucketNotFirstMaxTokensRows pins the walk
// shape. There is no owner index — records are keyed by HMAC hash and UserID
// lives inside the JSON — and once the cap is per owner the bucket can hold
// far more than auth.MaxTokens rows. So the count must decode EVERY row and
// count only the new token's owner; it may stop early once MaxTokens matches
// are found, never after MaxTokens rows.
//
// The fixture writes more than MaxTokens other-owner rows directly into the
// bucket under keys that sort BEFORE every hex hash key ('!' < '0'), so a
// walk that gives up after MaxTokens rows sees only strangers, counts zero
// for the target owner, and lets a 101st token through; a walk that counts
// rows instead of owners blocks the target owner's first.
func TestAgentTokenCap_CountsWholeBucketNotFirstMaxTokensRows(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"
	const stranger = "01HTEST00000000000STRANGER"
	const strangerRows = auth.MaxTokens + 50

	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		for i := 0; i < strangerRows; i++ {
			rec := auth.AgentToken{
				Name:        fmt.Sprintf("stranger-%03d", i),
				UserID:      stranger,
				TokenHash:   fmt.Sprintf("!stranger-%03d", i),
				TokenPrefix: "mcp_agt_strang",
				Permissions: []string{auth.PermRead},
				CreatedAt:   time.Now().UTC(),
			}
			data, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			// '!' (0x21) sorts before every hex digit, so these rows are the
			// first MaxTokens+50 the cursor yields.
			if err := bucket.Put([]byte(rec.TokenHash), data); err != nil {
				return err
			}
		}
		return nil
	}))

	// Positive control on the fixture: the strangers really are in the bucket
	// and really do outnumber the cap.
	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	require.Equal(t, strangerRows, count)

	// The target owner's full quota still mints ...
	fillOwnerToCap(t, mgr, ownerA)

	// ... and the 101st is refused, even though the first MaxTokens rows in
	// key order belong to someone else.
	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenLimitReached)
}

// TestAgentTokenCap_UnparseableRowIsSkippedNotCounted: a corrupt row must
// neither abort the create for every tenant nor be counted against anyone.
func TestAgentTokenCap_UnparseableRowIsSkippedNotCounted(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"

	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("!corrupt"), []byte("{not json"))
	}))

	fillOwnerToCap(t, mgr, ownerA)

	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenLimitReached)
}
