//go:build server

package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 107 FR-037 (issue #1177): the agent-token cap is enforced PER OWNER, and
// the 409 body names only the caller's own count. A global cap was a
// cross-tenant oracle twice over — one tenant could exhaust every tenant's
// slots, and the body told any tenant that the fleet total had reached
// auth.MaxTokens.
//
// These two tests replace TestCreateUserToken_CapExhaustionIsConflict and
// TestCreateUserToken_CapExhaustionDoesNotBlameTheCaller, which pinned the
// deployment-wide wording ("shared by all users … ask an administrator") that
// the per-owner cap makes false.

// assertOwnQuotaWording pins what the per-owner 409 body may and may not say:
// it states the limit, it addresses the caller's own tokens, and it never
// describes the deployment, other users or a fleet total.
func assertOwnQuotaWording(t *testing.T, msg string) {
	t.Helper()
	lower := strings.ToLower(msg)
	assert.Contains(t, msg, fmt.Sprintf("%d", auth.MaxTokens),
		"the cap message must say what the limit is")
	assert.Contains(t, lower, "your",
		"the cap is the caller's own quota; the body must say so")
	for _, leak := range []string{"deployment", "shared by all", "all users", "administrator", "other users"} {
		assert.NotContains(t, lower, leak,
			"a per-owner cap body must not describe anything beyond the caller's own tokens")
	}
}

// TestCreateUserToken_CapIsPerOwner: user A at the cap is refused with 409,
// and user B — who holds nothing — mints their first on the same router.
//
// Oracle discipline: A's 100th token mints through the HTTP route (positive
// control on the same router), so the 409 that follows is exhaustion and not a
// malformed body or an unwired store; B's success proves the cap is A's alone.
//
// BITES: restore the global `Stats().KeyN` count in storage and B's first
// create answers 409; restore the deployment-wide wording and the body
// assertions fail.
func TestCreateUserToken_CapIsPerOwner(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(userACtx())

	// Fill to one below the cap through storage, which is far faster than the
	// HTTP route and exercises the same counter.
	for i := 0; i < auth.MaxTokens-1; i++ {
		rig.seedToken(t, tokenUserA, fmt.Sprintf("filler-%03d", i))
	}

	last := rig.createToken(t, "last-slot", nil)
	require.Equal(t, http.StatusCreated, last.Code,
		"positive control: the final slot below the cap must still mint (%s)", last.Body.String())

	over := rig.createToken(t, "one-too-many", nil)
	require.Equal(t, http.StatusConflict, over.Code,
		"the token cap must answer 409, matching the personal edition (%s)", over.Body.String())
	assertOwnQuotaWording(t, errorMessage(t, over))

	// The cap is A's, not the deployment's.
	rig.actAs(userBCtx())
	first := rig.createToken(t, "b-first", nil)
	require.Equal(t, http.StatusCreated, first.Code,
		"user B holds no tokens; A's cap must not block B (%s)", first.Body.String())
}

// TestCreateUserToken_CapBodyNamesOnlyTheCallersCount is the oracle half on
// its own: user B, holding exactly one token, is told nothing about the 100
// tokens user A holds. The 409 only ever fires for B once B is at the cap,
// and its body then describes B's own quota.
//
// BITES: with the global count B's second create is refused while B holds one
// token, and with the old body the message describes the deployment.
func TestCreateUserToken_CapBodyNamesOnlyTheCallersCount(t *testing.T) {
	rig := newTokenTestRig(t)

	rig.actAs(userBCtx())
	ctrl := rig.createToken(t, "b-first", nil)
	require.Equal(t, http.StatusCreated, ctrl.Code,
		"positive control: user B must be able to mint before anyone fills anything (%s)", ctrl.Body.String())

	// User A fills A's OWN quota entirely.
	for i := 0; i < auth.MaxTokens; i++ {
		rig.seedToken(t, tokenUserA, fmt.Sprintf("a-filler-%03d", i))
	}

	second := rig.createToken(t, "b-second", nil)
	require.Equal(t, http.StatusCreated, second.Code,
		"A's full quota must not be reported to B as B's problem (%s)", second.Body.String())

	// Now B reaches B's own cap, and the refusal is about B.
	for i := 2; i < auth.MaxTokens; i++ {
		rig.seedToken(t, tokenUserB, fmt.Sprintf("b-filler-%03d", i))
	}
	over := rig.createToken(t, "b-one-too-many", nil)
	require.Equal(t, http.StatusConflict, over.Code, "B at B's own cap must be refused (%s)", over.Body.String())
	assertOwnQuotaWording(t, errorMessage(t, over))
}
