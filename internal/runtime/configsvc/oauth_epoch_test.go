package configsvc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 116 PR2b (#1607, data-model §2): the OAuth epoch of the committed view.
// It is G on create and replace, moves to G when the commit clears the
// server's OAuth state (OAuthCleared), and is kept on every other change, so
// the credential binding {Incarnation, OAuthEpoch} changes exactly when the
// credentials an instance holds stop belonging to the server.
func TestGenerations_OAuthEpochRule(t *testing.T) {
	svc := NewService(cfgWith(), "", zap.NewNop())
	require.NoError(t, svc.Update(cfgWith(srvEntry("acme"), srvEntry("other")), UpdateTypeModify, "create"))
	created, _ := svc.Current().ServerGeneration("acme")
	require.Equal(t, created.Gen, created.OAuthEpoch, "a created server's epoch is G")
	assert.Equal(t, created.Incarnation, created.Binding().Incarnation)
	assert.Equal(t, created.OAuthEpoch, created.Binding().OAuthEpoch)

	// An edit that does not clear OAuth keeps the epoch (and the binding).
	edited := cfgWith(srvEntry("acme", "x"), srvEntry("other"))
	p, err := svc.Prepare(PrepareInput{Config: edited})
	require.NoError(t, err)
	_, err = svc.Publish(p)
	require.NoError(t, err)
	e1, _ := svc.Current().ServerGeneration("acme")
	assert.Greater(t, e1.Gen, created.Gen)
	assert.Equal(t, created.Binding(), e1.Binding(), "a non-OAuth edit keeps the credential binding")

	// An OAuth config change the commit clears moves the epoch to G and keeps
	// the incarnation.
	oauthEdit := cfgWith(srvEntry("acme", "x"), srvEntry("other"))
	oauthEdit.Servers[0].OAuth = &config.OAuthConfig{Scopes: []string{"read"}}
	p, err = svc.Prepare(PrepareInput{Config: oauthEdit, OAuthCleared: map[string]bool{"acme": true}})
	require.NoError(t, err)
	_, err = svc.Publish(p)
	require.NoError(t, err)
	e2, _ := svc.Current().ServerGeneration("acme")
	assert.Equal(t, p.Generation(), e2.OAuthEpoch, "an OAuth clear moves the epoch to G")
	assert.Equal(t, created.Incarnation, e2.Incarnation, "an OAuth clear keeps the incarnation")
	assert.Equal(t, e2, p.Changes["acme"].New, "snapshot and change agree")
	other, _ := svc.Current().ServerGeneration("other")
	assert.Equal(t, created.Gen, other.OAuthEpoch, "an unrelated server keeps its epoch")

	// Remove and re-add in one commit: a new binding.
	p, err = svc.Prepare(PrepareInput{Config: oauthEdit, Replaced: map[string]bool{"acme": true}})
	require.NoError(t, err)
	rep := p.Changes["acme"].New
	assert.Equal(t, p.Generation(), rep.Incarnation)
	assert.Equal(t, p.Generation(), rep.OAuthEpoch)
}

// A row written before PR2b (epoch 0) keeps epoch 0 through a restart and
// unrelated edits, so records written before PR2b stay readable; a server
// with no stored stamp at all (a database written before Spec 116) gets an
// incarnation and keeps epoch 0 too, so an upgrade signs nothing out.
func TestGenerations_OAuthEpochLegacySeed(t *testing.T) {
	svc := NewService(cfgWith(srvEntry("legacy"), srvEntry("fresh")), "", zap.NewNop())
	svc.SeedVersion(10)
	p, err := svc.Prepare(PrepareInput{
		Config: cfgWith(srvEntry("legacy", "edited"), srvEntry("fresh")),
		Seed:   map[string]SeedStamp{"legacy": {Stamp: ServerGen{Gen: 5, Incarnation: 3}, Matches: false}},
	})
	require.NoError(t, err)
	legacy, _ := p.Snapshot().ServerGeneration("legacy")
	fresh, _ := p.Snapshot().ServerGeneration("fresh")
	assert.Equal(t, ServerGen{Gen: 11, Incarnation: 3, OAuthEpoch: 0}, legacy)
	assert.Equal(t, ServerGen{Gen: 11, Incarnation: 11, OAuthEpoch: 0}, fresh)
}
