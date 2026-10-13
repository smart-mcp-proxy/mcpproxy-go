package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 116 PR4, T4.1 (US4 scenario 1): a stdio instance parked after its last
// registry lookup and before it launches anything; a commit removes the
// server and returns; the instance is released. The launch marker is never
// created. Parent (PR3): the instance passed its connect-level check before
// the removal, so it launches the removed server's command.
func TestRetire_StdioLaunchParked_RemovalLaunchesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	m := newGenTestManager(t)
	cfg := markerCommand(t, "marker-srv", dir)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)

	parked, release := parkFirstAdmit(inst)
	defer release()
	errc := connectAsync(inst)
	<-parked

	// The competing commit: removal. It returns before the release.
	res := m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Present: false}})
	require.Len(t, res.Unregistered, 1)
	assert.True(t, inst.Retired(), "the commit retires the instance before it returns")
	assert.True(t, inst.Gate().Retired())

	release()
	err := waitErr(t, errc)
	require.Error(t, err, "a retired instance cannot connect")
	time.Sleep(300 * time.Millisecond) // a launched shell would have touched its marker by now
	assert.Zero(t, markers(t, dir), "the removed server's command must not run")
}

// Control: the same marker command does launch when nothing retires it, so a
// zero marker count above proves the barrier, not a broken fixture.
func TestRetire_StdioMarkerControl_LaunchesWhenNotRetired(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	m := newGenTestManager(t)
	cfg := markerCommand(t, "marker-ctl", dir)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)
	t.Cleanup(func() { _ = inst.Disconnect() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = inst.Connect(ctx) // the marker command is no MCP server: initialize fails
	require.Eventually(t, func() bool { return markers(t, dir) > 0 }, 5*time.Second, 50*time.Millisecond)
}

// T4.11 (review R2.4): a stdio launch held INSIDE its admitted section
// (after the child started, before its group is registered). Retire waits on
// the barrier; after the release the child is registered, the gate refuses
// to track it and kills it, and the connect fails.
func TestRetire_StdioLaunchInsideSection_ChildKilled(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a shell")
	}
	dir := t.TempDir()
	m := newGenTestManager(t)
	cfg := markerCommand(t, "marker-in", dir)
	m.AdvanceGenerations([]GenerationChange{present(cfg.Name, 1, cfg)})
	require.NoError(t, m.AddServerConfigAt(cfg.Name, cfg, 1))
	inst := registered(t, m, cfg.Name)

	inSection := make(chan struct{})
	resume := make(chan struct{})
	restore := setStdioLaunchHookForTest(func() {
		// Hold the section until the child has started its descendant, so
		// the kill below must reach the whole group.
		deadline := time.Now().Add(5 * time.Second)
		for descendants(t, dir) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		close(inSection)
		<-resume
	})
	defer restore()

	errc := connectAsync(inst)
	<-inSection
	require.Equal(t, 1, descendants(t, dir), "the child started its descendant")
	require.True(t, processAlive(t, dir), "control: the child and its descendant are visible to processAlive")

	removed := make(chan struct{})
	go func() {
		m.AdvanceGenerations([]GenerationChange{{Name: cfg.Name, Present: false}})
		close(removed)
	}()
	require.Eventually(t, inst.Gate().RetireWaiting, 5*time.Second, time.Millisecond,
		"Retire waits for the admitted launch section")
	select {
	case <-removed:
		t.Fatal("the commit returned while a launch section was open")
	default:
	}
	close(resume)
	<-removed

	err := waitErr(t, errc)
	require.Error(t, err, "the connect that was admitted before retirement fails")
	// The child and its descendant started but are dead: no process with
	// this run's marker dir in its arguments survives (a kill of the
	// immediate child alone would leave the descendant running).
	require.Eventually(t, func() bool { return !processAlive(t, dir) }, 3*time.Second, 50*time.Millisecond,
		"the launched child is killed by retirement")
}
