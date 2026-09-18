package storage

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Issue #1177 (merged as #1286; Spec 107 FR-037 records the outcome): the
// agent-token cap is two bounds, not one. Every OWNED token (non-empty UserID)
// is counted against auth.MaxTokensPerOwner for its owner, and every stored
// record — owned or ownerless, live or revoked — still counts against the
// deployment-wide auth.MaxTokens storage bound. Ownerless personal-edition
// tokens are exempt from the owner quota, so the personal edition is unchanged.
//
// agent_token_owner_quota_test.go pins the headline properties (26th owned
// token refused, another tenant still mints, permanent delete frees the slot,
// ownerless tokens keep the deployment cap). The tests here pin the SHAPE of
// the count — there is no owner index, so it is a decode-every-row walk — and
// the interaction between the two bounds.
//
// Oracle discipline for every test below: a positive control mints the token
// immediately BELOW the bound for the same owner, so the sentinel that follows
// is the bound and not a name collision or an unwired store.

// fillOwnerQuota mints exactly auth.MaxTokensPerOwner tokens for one owner and
// requires every one of them to land.
func fillOwnerQuota(t *testing.T, mgr *Manager, owner string) {
	t.Helper()
	for i := 0; i < auth.MaxTokensPerOwner; i++ {
		token, raw := makeOwnedTestToken(t, fmt.Sprintf("%s-fill-%03d", owner, i), owner)
		require.NoError(t, mgr.CreateAgentToken(token, raw, testHMACKey),
			"owner %q token %d must land below the owner quota", owner, i)
	}
}

// seedRawAgentTokenRows writes records straight into the agent_tokens bucket
// under keys that sort BEFORE every hex HMAC key ('!' is 0x21, below '0'), so
// they are the first rows any cursor yields.
func seedRawAgentTokenRows(t *testing.T, mgr *Manager, owner string, n int) {
	t.Helper()
	require.NoError(t, mgr.db.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(AgentTokensBucket))
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			rec := auth.AgentToken{
				Name:        fmt.Sprintf("%s-%03d", owner, i),
				UserID:      owner,
				TokenHash:   fmt.Sprintf("!%s-%03d", owner, i),
				TokenPrefix: "mcp_agt_seeded",
				Permissions: []string{auth.PermRead},
				CreatedAt:   time.Now().UTC(),
			}
			data, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte(rec.TokenHash), data); err != nil {
				return err
			}
		}
		return nil
	}))
}

// TestAgentTokenOwnerQuota_CountsMatchesNotRows pins the walk shape. Records
// are keyed by HMAC hash and UserID lives inside the JSON, so the owner count
// must decode EVERY row and count only the new token's owner. The fixture puts
// more than auth.MaxTokensPerOwner rows of a stranger's at the FRONT of key
// order: a walk that counted rows instead of owners would refuse the target
// owner's first token, and one that gave up after MaxTokensPerOwner rows would
// see only strangers, count zero, and let the 26th through.
func TestAgentTokenOwnerQuota_CountsMatchesNotRows(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"
	const stranger = "01HTEST00000000000STRANGER"
	const strangerRows = auth.MaxTokensPerOwner + 5

	seedRawAgentTokenRows(t, mgr, stranger, strangerRows)

	// Positive control on the fixture: the strangers really are in the bucket
	// and really do outnumber the owner quota.
	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	require.Equal(t, strangerRows, count)

	// The target owner's full quota still mints ...
	fillOwnerQuota(t, mgr, ownerA)

	// ... and the next one is refused as THE OWNER's quota, even though the
	// first rows in key order belong to someone else.
	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenOwnerLimitReached)
}

// TestAgentTokenOwnerQuota_UnparseableRowIsSkippedNotCounted: a corrupt row
// must neither abort the create for every tenant nor be counted against anyone.
func TestAgentTokenOwnerQuota_UnparseableRowIsSkippedNotCounted(t *testing.T) {
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

	fillOwnerQuota(t, mgr, ownerA)

	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenOwnerLimitReached)
}

// TestAgentTokenOwnerQuota_OwnerlessAndOwnedDoNotShareAQuota: the operator's
// ownerless tokens are exempt from the owner quota and never count against a
// user's, and a user at their quota does not stop an ownerless mint.
func TestAgentTokenOwnerQuota_OwnerlessAndOwnedDoNotShareAQuota(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	const ownerA = "01HTEST0000000000000USERA"

	// More ownerless tokens than any single owner may hold.
	for i := 0; i < auth.MaxTokensPerOwner+5; i++ {
		token, raw := makeOwnedTestToken(t, fmt.Sprintf("ownerless-%03d", i), "")
		require.NoError(t, mgr.CreateAgentToken(token, raw, testHMACKey),
			"ownerless tokens are exempt from the owner quota")
	}

	fillOwnerQuota(t, mgr, ownerA)
	over, raw := makeOwnedTestToken(t, "a-one-too-many", ownerA)
	require.ErrorIs(t, mgr.CreateAgentToken(over, raw, testHMACKey), ErrAgentTokenOwnerLimitReached,
		"the ownerless rows must not have been counted for the user, and the user's own quota still binds")

	after, raw := makeOwnedTestToken(t, "ownerless-after", "")
	require.NoError(t, mgr.CreateAgentToken(after, raw, testHMACKey),
		"a user at their quota must not block an ownerless mint below the deployment cap")
}

// TestAgentTokenCap_DeploymentBoundStillApplies: the owner quota is enforced
// in ADDITION to the deployment-wide storage bound, not instead of it. Enough
// owners at their quota fill the deployment, and the next owner's FIRST token
// is refused with the deployment sentinel — the owner quota was not reached.
func TestAgentTokenCap_DeploymentBoundStillApplies(t *testing.T) {
	mgr, cleanup := setupTestStorageForAgentTokens(t)
	defer cleanup()

	owners := auth.MaxTokens / auth.MaxTokensPerOwner
	require.Equal(t, auth.MaxTokens, owners*auth.MaxTokensPerOwner,
		"fixture assumes the deployment cap is a whole number of owner quotas")
	for i := 0; i < owners; i++ {
		fillOwnerQuota(t, mgr, fmt.Sprintf("01HTEST000000000000OWNER%02d", i))
	}

	count, err := mgr.GetAgentTokenCount()
	require.NoError(t, err)
	require.Equal(t, auth.MaxTokens, count, "positive control: the deployment is exactly full")

	first, raw := makeOwnedTestToken(t, "late-first", "01HTEST0000000000000LATE")
	require.ErrorIs(t, mgr.CreateAgentToken(first, raw, testHMACKey), ErrAgentTokenLimitReached,
		"a full deployment refuses with the deployment sentinel, not the owner one")

	ownerless, raw := makeOwnedTestToken(t, "ownerless-late", "")
	require.ErrorIs(t, mgr.CreateAgentToken(ownerless, raw, testHMACKey), ErrAgentTokenLimitReached,
		"the deployment bound applies to ownerless tokens too")
}
