package configsvc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func cfgWith(servers ...*config.ServerConfig) *config.Config {
	return &config.Config{Servers: servers}
}

func srvEntry(name string, args ...string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, Command: "true", Protocol: "stdio", Args: args}
}

// T017 (T1.9): the generation rule table of data-model §2.
func TestGenerations_RuleTable(t *testing.T) {
	svc := NewService(cfgWith(), "", zap.NewNop())

	require.NoError(t, svc.Update(cfgWith(srvEntry("a"), srvEntry("b")), UpdateTypeModify, "create"))
	s1 := svc.Current()
	ga, _ := s1.ServerGeneration("a")
	assert.Equal(t, ServerGen{Gen: s1.Version, Incarnation: s1.Version}, ga, "created: {G, G}")

	// Unrelated change to b keeps a's generation; b keeps its incarnation.
	p, err := svc.Prepare(PrepareInput{Config: cfgWith(srvEntry("a"), srvEntry("b", "x"))})
	require.NoError(t, err)
	snap, err := svc.Publish(p)
	require.NoError(t, err)
	a2, _ := snap.ServerGeneration("a")
	b2, _ := snap.ServerGeneration("b")
	assert.Equal(t, ga, a2, "unchanged entry keeps its generation")
	assert.Equal(t, snap.Version, b2.Gen, "changed entry gets G")
	gb, _ := s1.ServerGeneration("b")
	assert.Equal(t, gb.Incarnation, b2.Incarnation, "an update keeps the incarnation")
	require.Contains(t, p.Changes, "b")
	assert.Equal(t, ChangeUpdated, p.Changes["b"].Kind)
	assert.True(t, p.Changes["b"].Reconnect, "args changed: reconnect")
	assert.NotContains(t, p.Changes, "a")

	// Remove b, then re-create it in a later commit: new incarnation.
	require.NoError(t, svc.Update(cfgWith(srvEntry("a")), UpdateTypeModify, "remove"))
	_, ok := svc.Current().ServerGeneration("b")
	assert.False(t, ok, "removed name is dropped")
	require.NoError(t, svc.Update(cfgWith(srvEntry("a"), srvEntry("b", "x")), UpdateTypeModify, "recreate"))
	b4, _ := svc.Current().ServerGeneration("b")
	assert.Greater(t, b4.Incarnation, b2.Incarnation, "a re-created name gets a new incarnation")

	// A generation-only change (disabled_tools) does not reconnect.
	next := cfgWith(srvEntry("a"), srvEntry("b", "x"))
	next.Servers[1].DisabledTools = []string{"t"}
	p, err = svc.Prepare(PrepareInput{Config: next})
	require.NoError(t, err)
	assert.False(t, p.Changes["b"].Reconnect, "a non-connection edit is not a reconnect")
}

// T1.17 (review R8.2): a one-commit remove and re-create is Replaced with a
// new incarnation, whether or not the re-created entry equals the removed one.
func TestGenerations_ReplacedSameName(t *testing.T) {
	for _, changed := range []bool{false, true} {
		svc := NewService(cfgWith(), "", zap.NewNop())
		require.NoError(t, svc.Update(cfgWith(srvEntry("acme")), UpdateTypeModify, "create"))
		old, _ := svc.Current().ServerGeneration("acme")
		re := srvEntry("acme")
		if changed {
			re.Args = []string{"new"}
		}
		p, err := svc.Prepare(PrepareInput{Config: cfgWith(re), Replaced: map[string]bool{"acme": true}})
		require.NoError(t, err)
		ch := p.Changes["acme"]
		assert.Equal(t, ChangeReplaced, ch.Kind, "changed=%v", changed)
		assert.Equal(t, old, ch.Old)
		assert.Equal(t, ServerGen{Gen: p.Generation(), Incarnation: p.Generation()}, ch.New)
		assert.NotEqual(t, old.Incarnation, ch.New.Incarnation)
		assert.True(t, ch.Reconnect)
		got, _ := p.Snapshot().ServerGeneration("acme")
		assert.Equal(t, ch.New, got, "snapshot and change agree")
	}

	svc := NewService(cfgWith(srvEntry("a")), "", zap.NewNop())
	_, err := svc.Prepare(PrepareInput{Config: cfgWith(srvEntry("a")), Replaced: map[string]bool{"ghost": true}})
	require.Error(t, err, "a replaced name absent from base or next is an invariant error")
}

// T1.9: SeedVersion makes the next generation exceed every persisted one and
// never lowers the counter; a seeded matching row keeps its stamp.
func TestGenerations_SeedVersionAndSeedStamps(t *testing.T) {
	svc := NewService(cfgWith(srvEntry("a"), srvEntry("b")), "", zap.NewNop())
	svc.SeedVersion(41)
	svc.SeedVersion(7)
	p, err := svc.Prepare(PrepareInput{
		Config: cfgWith(srvEntry("a"), srvEntry("b")),
		Seed: map[string]SeedStamp{
			"a": {Stamp: ServerGen{Gen: 30, Incarnation: 12}, Matches: true},
			"b": {Stamp: ServerGen{Gen: 31, Incarnation: 13}, Matches: false},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), p.Generation())
	a, _ := p.Snapshot().ServerGeneration("a")
	b, _ := p.Snapshot().ServerGeneration("b")
	assert.Equal(t, ServerGen{Gen: 30, Incarnation: 12}, a, "a matching stored row keeps its stamps across a restart")
	assert.Equal(t, ServerGen{Gen: 42, Incarnation: 13}, b, "a differing stored row keeps its incarnation and gets G")
}

func TestPublish_RejectsConcurrentPublish(t *testing.T) {
	svc := NewService(cfgWith(), "", zap.NewNop())
	p, err := svc.Prepare(PrepareInput{Config: cfgWith(srvEntry("a"))})
	require.NoError(t, err)
	require.NoError(t, svc.Update(cfgWith(srvEntry("b")), UpdateTypeModify, "other"))
	_, err = svc.Publish(p)
	require.True(t, errors.Is(err, ErrConcurrentPublish))
}
