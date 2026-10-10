package core

import "errors"

// ErrDialRefused is returned when the owner of this client (the managed client,
// on behalf of the upstream manager) refuses a launch or dial because the client
// was retired: removed or replaced by a newer configuration commit.
var ErrDialRefused = errors.New("dial refused: client retired")

// DialGate admits one launch or dial attempt. It returns ok=false once the
// client is retired. When ok is true the caller MUST call release when the
// launch has been issued (the child process started); the owner's Retire waits
// for outstanding admissions, so after Retire returns no new process or
// connection can start, and any already started is visible to the owner's
// subsequent teardown.
type DialGate func() (release func(), ok bool)

// SetDialGate installs the gate consulted immediately before this client spawns
// a child process or opens its transport. Cancelling a context alone does not
// stop a stdio startup (it runs on a background context), so retirement has to
// be synchronized with the actual launch.
func (c *Client) SetDialGate(g DialGate) {
	c.dialGate.Store(&g)
}

// BeforeLaunchHook is a test seam fired just before the launch admission is
// consulted for a process spawn, i.e. after every context check on the way and
// immediately before the child would start.
var BeforeLaunchHook func(serverName string)

// AfterLaunchHook is a test seam fired after a child process was actually
// started (a spawn the gate admitted).
var AfterLaunchHook func(serverName string)

// admitDial consults the gate. With no gate installed it admits unconditionally.
func (c *Client) admitDial() (release func(), err error) {
	g := c.dialGate.Load()
	if g == nil || *g == nil {
		return func() {}, nil
	}
	rel, ok := (*g)()
	if !ok {
		return nil, ErrDialRefused
	}
	if rel == nil {
		rel = func() {}
	}
	return rel, nil
}

// admitSpawn is admitDial for a process spawn; it fires BeforeLaunchHook first.
func (c *Client) admitSpawn() (release func(), err error) {
	if BeforeLaunchHook != nil {
		BeforeLaunchHook(c.config.Name)
	}
	return c.admitDial()
}
