//go:build server

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// mutationRoutes are the three owner-scoped mutators that used to do a preflight
// read before their transaction.
var mutationRoutes = []struct {
	name   string
	method string
	path   func(token string) string
}{
	{"revoke", http.MethodDelete, func(tok string) string { return "/api/v1/user/tokens/" + tok }},
	{"delete", http.MethodDelete, func(tok string) string { return "/api/v1/user/tokens/" + tok + "/permanent" }},
	{"regenerate", http.MethodPost, func(tok string) string { return "/api/v1/user/tokens/" + tok + "/regenerate" }},
}

// TestUserTokenMutators_NotFoundLeaksNoStorageSentinel pins the two things the
// preflight got wrong at once.
//
// The old shape was: GetAgentTokenByOwnerAndName, then a SEPARATE mutating
// transaction. If the (owner, name) pair vanished between the two calls, the
// mutator's storage.ErrAgentTokenNotFound fell through to the generic branch,
// whose body was fmt.Sprintf("Failed to … token: %v", err) — so the response
// echoed the storage sentinel's text at 500. The resolve now happens inside the
// mutating transaction and its error is classified, so the same condition is a
// 404 whose body is byte-identical to an ordinary absent token's.
//
// Oracle discipline: this does NOT assert "the body does not contain the token
// name" — a 404 echoes the caller's own path parameter, so that would pass
// vacuously. It asserts STATUS PARITY against a name that exists nowhere, plus
// an exact-status pin, plus a positive control proving the fixture and the route
// really work on the same router.
//
// BITES: reinstate the preflight and its `fmt.Sprintf("Failed to revoke token:
// %v", err)` fall-through, and the storage-sentinel assertions below fail.
func TestUserTokenMutators_NotFoundLeaksNoStorageSentinel(t *testing.T) {
	for _, route := range mutationRoutes {
		t.Run(route.name, func(t *testing.T) {
			rig := newTokenTestRig(t)
			rig.actAs(userACtx())

			// Positive control on the SAME router: the route works, and the
			// fixture lands, so a 404 below is about the token and not the path.
			rig.seedToken(t, tokenUserA, "control")
			ok := rig.call(t, route.method, route.path("control"))
			require.Equal(t, http.StatusOK, ok.Code,
				"positive control: %s must succeed on the caller's own token (%s)", route.name, ok.Body.String())

			// Two ways to be unresolvable: owned by someone else, and nowhere.
			rig.seedToken(t, tokenUserB, "b-only")
			foreign := rig.call(t, route.method, route.path("b-only"))
			absent := rig.call(t, route.method, route.path("no-such-token-Zq7"))

			assertHandlerNotFound(t, foreign, route.name+" on another tenant's token")
			assertHandlerNotFound(t, absent, route.name+" on an absent token")

			// Status parity between the two, pinned to exactly 404.
			require.Equal(t, http.StatusNotFound, foreign.Code)
			require.Equal(t, http.StatusNotFound, absent.Code)
			assert.Equal(t, absent.Code, foreign.Code,
				"%s: another tenant's token must be indistinguishable from an absent one", route.name)

			// And no storage internals in either body.
			for _, w := range []*httptest.ResponseRecorder{foreign, absent} {
				body := w.Body.String()
				assert.NotContains(t, body, storage.ErrAgentTokenNotFound.Error(),
					"%s: the response must not echo the storage sentinel", route.name)
				assert.NotContains(t, body, "agent token",
					"%s: the response must not echo storage's wording", route.name)
			}

			// The other tenant's token really is untouched — the 404 is a
			// refusal, not a silent success.
			survivor, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserB, "b-only")
			require.NoError(t, err)
			require.NotNil(t, survivor, "another tenant's token must still exist")
			assert.False(t, survivor.Revoked, "another tenant's token must not have been revoked")
		})
	}
}

// TestCreateUserToken_CapExhaustionIsConflict pins the status the token cap
// answers with. The same storage condition (storage.ErrAgentTokenLimitReached)
// used to map to 409 on the personal-edition surface and 503 on this one, so a
// client's retry behaviour depended on which door it knocked on — and 503
// invites a retry loop against a condition that will never clear on its own.
//
// Oracle discipline: a positive control mints the token immediately BELOW the
// cap on the same router, so the 409 that follows is exhaustion and not a
// malformed body or an unwired store.
//
// BITES: restore http.StatusServiceUnavailable in createUserToken.
func TestCreateUserToken_CapExhaustionIsConflict(t *testing.T) {
	rig := newTokenTestRig(t)
	rig.actAs(userACtx())

	// Fill to one below the cap through storage, which is far faster than the
	// HTTP route and exercises the same counter.
	for i := 0; i < auth.MaxTokens-1; i++ {
		rig.seedToken(t, tokenUserA, fmt.Sprintf("filler-%03d", i))
	}

	// Positive control: the last slot below the cap still mints.
	last := rig.createToken(t, "last-slot", nil)
	require.Equal(t, http.StatusCreated, last.Code,
		"positive control: the final slot below the cap must still mint (%s)", last.Body.String())

	over := rig.createToken(t, "one-too-many", nil)
	require.Equal(t, http.StatusConflict, over.Code,
		"the token cap must answer 409, matching the personal edition (%s)", over.Body.String())

	msg := errorMessage(t, over)
	assert.Contains(t, msg, fmt.Sprintf("%d", auth.MaxTokens),
		"the cap message must say what the limit is")

	// The cap is DEPLOYMENT-wide (auth.MaxTokens is counted across the whole
	// agent_tokens bucket, all owners together), and there is no per-owner
	// quota — that is issue #1177. So the body must not read as the caller's
	// own quota: a tenant who holds none of the tokens filling it would go
	// hunting for tokens of theirs to delete, and deleting every one of them
	// need not free a slot. It has to say whose limit it is and who can act.
	assert.Contains(t, strings.ToLower(msg), "administrator",
		"the cap body must point the caller at someone who can actually act on it")
	assert.NotRegexp(t, `(?i)\byou(r)? have reached|delete (one of )?your`, msg,
		"the cap body must not instruct the caller to free a slot they may not control")
}

// TestCreateUserToken_CapExhaustionDoesNotBlameTheCaller is the Q3 property on
// its own, with the victim being someone who holds NO tokens at all: the cap is
// filled entirely by another tenant.
//
// Oracle discipline: user B mints successfully at the start (positive control on
// the same router), so the 409 that follows is the deployment cap and not an
// unwired store; and the message is asserted for what it must NOT claim, since
// the defect is a true statement about the wrong subject.
//
// BITES: with the old body this fails on "administrator", because the message
// read "Maximum number of agent tokens (100) reached" — the personal edition's
// wording, where the caller does own every token.
func TestCreateUserToken_CapExhaustionDoesNotBlameTheCaller(t *testing.T) {
	rig := newTokenTestRig(t)

	// Positive control: user B can mint before the cap is filled.
	rig.actAs(userBCtx())
	ctrl := rig.createToken(t, "b-first", nil)
	require.Equal(t, http.StatusCreated, ctrl.Code,
		"positive control: user B must be able to mint before the cap fills (%s)", ctrl.Body.String())

	// User A fills the rest of the DEPLOYMENT-wide cap.
	for i := 0; i < auth.MaxTokens-1; i++ {
		rig.seedToken(t, tokenUserA, fmt.Sprintf("a-filler-%03d", i))
	}

	over := rig.createToken(t, "b-second", nil)
	require.Equal(t, http.StatusConflict, over.Code,
		"the cap must be reported to the blocked tenant (%s)", over.Body.String())

	// User B holds exactly one token. Deleting it frees one slot out of a cap
	// filled by someone else's 99 — which is precisely why the body must not
	// tell them to.
	msg := errorMessage(t, over)
	assert.Contains(t, strings.ToLower(msg), "shared by all users",
		"the body must say the limit is not the caller's own")
	assert.Contains(t, strings.ToLower(msg), "administrator",
		"the body must name who can act on a deployment-wide limit")
}

// TestRegenerateUserToken_ReNarrowsScopeToCurrentEntitlement pins the one
// re-check a token's server scope ever gets.
//
// resolveTokenServerScope runs at mint time and nothing revalidates afterwards,
// so un-sharing a server leaves every token already scoped to it holding that
// grant, and a token minted with a literal "*" before this branch existed still
// carries the star the enforcement layer honours unconditionally. Rotation is
// the moment the owner's current entitlement is in hand, so it is where the
// standing grant gets trimmed.
//
// Oracle discipline: the assertion is on the PERSISTED record, read back through
// storage — a response body could show a narrowed list while storage kept the
// wide one. A positive control first proves the entitled half survives, so an
// empty result cannot pass for a narrowing.
//
// BITES: drop the narrowScopeToEntitled hook from regenerateUserToken and the
// reread still shows the un-entitled name (and the bare "*").
func TestRegenerateUserToken_ReNarrowsScopeToCurrentEntitlement(t *testing.T) {
	rig := newTokenTestRigWithServers(t, scopeFixtureServers())
	rig.actAs(userACtx())
	rig.seedPersonalServer(t, tokenUserA, scopeAPersonal)

	// A token minted before the constraint existed: a literal star, plus a name
	// its owner is not (or is no longer) entitled to.
	seedTokenWithScope(t, rig, tokenUserA, "legacy", []string{"*", scopePrivateServer, scopeAPersonal})

	regen := rig.call(t, http.MethodPost, "/api/v1/user/tokens/legacy/regenerate")
	require.Equal(t, http.StatusOK, regen.Code, "rotation must not be refused (%s)", regen.Body.String())

	var resp AgentTokenResponse
	require.NoError(t, json.Unmarshal(regen.Body.Bytes(), &resp))

	// Read the PERSISTED record: that is the credential the enforcement layer
	// will consult, and it is what a body-only assertion would miss.
	stored, err := rig.store.GetAgentTokenByOwnerAndName(tokenUserA, "legacy")
	require.NoError(t, err)
	require.NotNil(t, stored)

	for _, scope := range [][]string{resp.AllowedServers, stored.AllowedServers} {
		assert.NotContains(t, scope, "*",
			"a tenant's literal star must be materialised on rotation, never persisted")
		assert.NotContains(t, scope, scopePrivateServer,
			"rotation must drop a server the owner is not entitled to")
		// Positive half: the entitled names survive, so this is a narrowing and
		// not a blanket wipe that would pass every assertion above.
		assert.Contains(t, scope, scopeAPersonal, "the owner's own server must survive rotation")
		assert.Contains(t, scope, scopeSharedServer, "a still-shared server must survive rotation")
	}

	// Rotation is idempotent: a second one changes nothing.
	again := rig.call(t, http.MethodPost, "/api/v1/user/tokens/legacy/regenerate")
	require.Equal(t, http.StatusOK, again.Code)
	var resp2 AgentTokenResponse
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &resp2))
	assert.ElementsMatch(t, resp.AllowedServers, resp2.AllowedServers,
		"re-narrowing an already-narrowed scope must be a no-op")
}

// seedTokenWithScope writes a token with an explicit owner AND server scope,
// which the HTTP create route would now refuse — that is the point: it stands in
// for a record minted before the constraint existed.
func seedTokenWithScope(t *testing.T, rig *tokenTestRig, owner, name string, allowedServers []string) {
	t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, rig.store.CreateAgentToken(auth.AgentToken{
		Name:           name,
		UserID:         owner,
		AllowedServers: allowedServers,
		Permissions:    []string{auth.PermRead},
	}, raw, tokenTestHMACKey))

	// Fixture check: the wide scope really landed, or every assertion about
	// narrowing it would be vacuous.
	seeded, err := rig.store.GetAgentTokenByOwnerAndName(owner, name)
	require.NoError(t, err)
	require.NotNil(t, seeded, "fixture: the seeded token must exist")
	require.Contains(t, seeded.AllowedServers, "*", "fixture: the pre-branch star must be persisted")
}
