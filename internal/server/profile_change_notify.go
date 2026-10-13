package server

import (
	"reflect"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// FR-027 (Spec 108-f F9, F10): when a profile's policy or servers change - by a
// profiles-service write or by a hand edit of the config file - every live MCP
// session it governs is sent notifications/tools/list_changed so its client
// re-lists tools, and one profiles.changed event per changed profile is
// published for UIs to refetch.
//
// One observer covers both origins: the config service's pre-publish observer
// sees every snapshot (service writes, file-watcher reloads), computes the
// delta against the snapshot it replaces and ENQUEUES it. A single worker
// goroutine sends only after currentConfig() is the new snapshot, so a client
// that re-lists tools on the notification can never read the OLD tool set, and
// nothing is ever sent from inside the config update mutex.

// profilesChange is one changed profile, for the profiles.changed event.
type profilesChange struct {
	name   string
	change string // create | update | delete | anonymous
}

// profileDelta is what changed between two config snapshots.
type profileDelta struct {
	// notify holds the profiles whose policy (fingerprint), effective servers or
	// existence changed: the sessions they govern need tools/list_changed.
	notify map[string]bool
	// anonymous is set when anonymous_profile changed: every anonymous session
	// is re-listed.
	anonymous bool
	// events is the profiles.changed stream (any stored-field change, incl.
	// display-only ones, invalidates a UI).
	events []profilesChange
}

func (d profileDelta) empty() bool {
	return len(d.notify) == 0 && !d.anonymous && len(d.events) == 0
}

func (d *profileDelta) merge(o profileDelta) {
	if d.notify == nil {
		d.notify = map[string]bool{}
	}
	for n := range o.notify {
		d.notify[n] = true
	}
	d.anonymous = d.anonymous || o.anonymous
	d.events = append(d.events, o.events...)
}

// computeProfileDelta compares two published snapshots.
func computeProfileDelta(oldCfg, newCfg *config.Config) profileDelta {
	d := profileDelta{notify: map[string]bool{}}
	if oldCfg == nil || newCfg == nil {
		return d
	}
	oldBy := map[string]*config.ProfileConfig{}
	for i := range oldCfg.Profiles {
		if _, dup := oldBy[oldCfg.Profiles[i].Name]; !dup {
			oldBy[oldCfg.Profiles[i].Name] = &oldCfg.Profiles[i]
		}
	}
	seen := map[string]bool{}
	for i := range newCfg.Profiles {
		p := &newCfg.Profiles[i]
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		o, existed := oldBy[p.Name]
		switch {
		case !existed:
			d.notify[p.Name] = true
			d.events = append(d.events, profilesChange{name: p.Name, change: "create"})
		case policyOrServersChanged(o, p, oldCfg, newCfg):
			d.notify[p.Name] = true
			d.events = append(d.events, profilesChange{name: p.Name, change: "update"})
		case !reflect.DeepEqual(*o, *p):
			d.events = append(d.events, profilesChange{name: p.Name, change: "update"}) // display-only edit
		}
	}
	var gone []string
	for n := range oldBy {
		if !seen[n] {
			gone = append(gone, n)
		}
	}
	sort.Strings(gone)
	for _, n := range gone {
		d.notify[n] = true
		d.events = append(d.events, profilesChange{name: n, change: "delete"})
	}
	if oldCfg.AnonymousProfile != newCfg.AnonymousProfile {
		d.anonymous = true
		d.events = append(d.events, profilesChange{name: newCfg.AnonymousProfile, change: "anonymous"})
	}
	return d
}

func policyOrServersChanged(o, n *config.ProfileConfig, oldCfg, newCfg *config.Config) bool {
	if profile.Compile(o).Fingerprint != profile.Compile(n).Fingerprint {
		return true
	}
	oldServers, newServers := o.EffectiveServers(oldCfg), n.EffectiveServers(newCfg)
	sort.Strings(oldServers)
	sort.Strings(newServers)
	return !reflect.DeepEqual(oldServers, newServers)
}

// profileChangeNotifier owns the queue and the single worker.
type profileChangeNotifier struct {
	p       *MCPProxyServer
	publish func(name, change string)

	mu      sync.Mutex
	pending *pendingProfileDelta
	// seq numbers every observed snapshot and recent remembers the newest few,
	// EVEN those whose delta is empty: a batch is delivered once the runtime's
	// current snapshot is the batch's or any LATER one, so an unrelated
	// publication racing a profile edit cannot strand the batch (F5.1).
	seq    uint64
	recent []seqSnapshot
	// holds counts in-flight session-rewriting writes (a rename): delivery waits
	// for them, so a client re-listing on tools/list_changed never resolves a
	// selection the rename has not yet rewritten (F5.2).
	holds int
	// delivering is set, atomically with the held() check, while the worker
	// delivers a batch. hold() waits for it to clear, so a rename cannot begin
	// between the worker's check and its sends (the batch it is delivering was
	// published before the rename began) (F1.3).
	delivering bool
	idle       *sync.Cond // signalled when delivering clears
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	// waitFor bounds how long the worker waits for a snapshot to be published
	// before dropping its delta (the write that produced it failed to publish).
	waitFor time.Duration
}

// recentSnapshotCap bounds the remembered snapshots. A batch older than this
// many publications is accepted on the next poll by falling back to "anything
// newer than its own sequence number".
const recentSnapshotCap = 64

type seqSnapshot struct {
	seq uint64
	cfg *config.Config
}

type pendingProfileDelta struct {
	cfg   *config.Config // the newest snapshot this batch is waiting for
	seq   uint64         // its sequence number
	delta profileDelta
}

func newProfileChangeNotifier(p *MCPProxyServer, publish func(name, change string)) *profileChangeNotifier {
	n := &profileChangeNotifier{
		p: p, publish: publish,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		waitFor: 5 * time.Second,
	}
	n.idle = sync.NewCond(&n.mu)
	return n
}

// start runs the worker. It stops with close().
func (n *profileChangeNotifier) start() {
	go n.run()
}

// close stops the worker and waits for it.
func (n *profileChangeNotifier) close() {
	select {
	case <-n.stop:
		return
	default:
		close(n.stop)
	}
	<-n.done
}

// enqueue records the delta between two snapshots. It never blocks: it runs
// under the config update mutex.
func (n *profileChangeNotifier) enqueue(oldCfg, newCfg *config.Config) {
	d := computeProfileDelta(oldCfg, newCfg)
	n.mu.Lock()
	n.seq++
	seq := n.seq
	n.recent = append(n.recent, seqSnapshot{seq: seq, cfg: newCfg})
	if len(n.recent) > recentSnapshotCap {
		n.recent = n.recent[len(n.recent)-recentSnapshotCap:]
	}
	if d.empty() {
		n.mu.Unlock()
		return // nothing to deliver; the snapshot is remembered for batches in flight
	}
	if n.pending == nil {
		n.pending = &pendingProfileDelta{cfg: newCfg, seq: seq}
	}
	n.pending.cfg, n.pending.seq = newCfg, seq // a newer snapshot merges into the batch
	n.pending.delta.merge(d)
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// publishedAtOrAfter reports the runtime's current snapshot when it is the
// batch's snapshot or one observed after it.
func (n *profileChangeNotifier) publishedAtOrAfter(batch *pendingProfileDelta) (*config.Config, bool) {
	cur := n.p.currentConfig()
	if cur == batch.cfg {
		return cur, true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, s := range n.recent {
		if s.seq > batch.seq && s.cfg == cur {
			return cur, true
		}
	}
	return nil, false
}

// hold defers delivery until the returned release runs. It first waits for a
// delivery already in flight to finish, so delivery and a hold never overlap.
func (n *profileChangeNotifier) hold() (release func()) {
	n.mu.Lock()
	for n.delivering {
		n.idle.Wait()
	}
	n.holds++
	n.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			n.mu.Lock()
			n.holds--
			n.mu.Unlock()
		})
	}
}

func (n *profileChangeNotifier) held() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.holds > 0
}

// beginDelivery atomically checks that no write holds delivery and marks a
// delivery in flight.
func (n *profileChangeNotifier) beginDelivery() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.holds > 0 {
		return false
	}
	n.delivering = true
	return true
}

func (n *profileChangeNotifier) endDelivery() {
	n.mu.Lock()
	n.delivering = false
	n.mu.Unlock()
	n.idle.Broadcast()
}

func (n *profileChangeNotifier) take() *pendingProfileDelta {
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.pending
	n.pending = nil
	return b
}

func (n *profileChangeNotifier) run() {
	defer close(n.done)
	for {
		select {
		case <-n.stop:
			return
		case <-n.wake:
		}
		batch := n.take()
		if batch == nil {
			continue
		}
		if !n.awaitPublished(batch) {
			continue
		}
		n.deliver(batch)
		n.endDelivery()
	}
}

// awaitPublished waits until the runtime's current snapshot IS the batch's
// newest snapshot or a later one and no rename holds delivery, merging any
// newer delta that arrives meanwhile. The time a write holds delivery does not
// count against the wait (F1.2). On true the caller is delivering (see
// beginDelivery) and must call endDelivery; it reports false when the wait times
// out or the notifier stops.
func (n *profileChangeNotifier) awaitPublished(batch *pendingProfileDelta) bool {
	deadline := time.Now().Add(n.waitFor)
	for {
		if more := n.take(); more != nil {
			batch.cfg, batch.seq = more.cfg, more.seq
			batch.delta.merge(more.delta)
		}
		if n.held() {
			deadline = time.Now().Add(n.waitFor) // a rename's hold is not a failed publish
		} else if cur, ok := n.publishedAtOrAfter(batch); ok && n.beginDelivery() {
			batch.cfg = cur
			return true
		}
		if time.Now().After(deadline) {
			if n.p.logger != nil {
				n.p.logger.Warn("profile change was never published; its notifications are dropped")
			}
			return false
		}
		select {
		case <-n.stop:
			return false
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// deliver publishes the profiles.changed events and notifies the sessions.
func (n *profileChangeNotifier) deliver(batch *pendingProfileDelta) {
	if n.publish != nil {
		for _, e := range batch.delta.events {
			n.publish(e.name, e.change)
		}
	}
	n.p.notifyProfileDelta(batch.cfg, batch.delta)
}

// notifyProfileDelta sends tools/list_changed to every live session whose
// latest effective profile or whose base (derived NOW: the token's current pin,
// or anonymous_profile for a session with no token) is in the delta. It never
// stores the base, so a rename or reassignment cannot leave a stale one.
func (p *MCPProxyServer) notifyProfileDelta(cfg *config.Config, d profileDelta) {
	if p.sessionStore == nil || (len(d.notify) == 0 && !d.anonymous) {
		return
	}
	pins := map[string]string{}
	pinsKnown := false
	if p.storage != nil {
		if tokens, err := p.storage.ListAgentTokens(); err == nil {
			pinsKnown = true
			for i := range tokens {
				if tokens[i].UserID == "" {
					pins[tokens[i].Name] = tokens[i].ProfilePin
				}
			}
		} else if p.logger != nil {
			p.logger.Warn("cannot read token pins for a profile change notification; notifying every token session", zap.Error(err))
		}
	}
	anon := ""
	if cfg != nil {
		anon = cfg.AnonymousProfile
	}
	for _, target := range p.sessionStore.SessionsMatching(func(info *SessionInfo) bool {
		if info.Profile != "" && d.notify[info.Profile] {
			return true
		}
		switch {
		case info.TokenName != "":
			if !pinsKnown {
				return true // fail toward notifying
			}
			pin := pins[info.TokenName]
			return pin != "" && d.notify[pin]
		case info.Anonymous:
			return d.anonymous || (anon != "" && d.notify[anon])
		}
		return false
	}) {
		p.sendToolsListChanged(target)
	}
}

// HoldProfileNotifications implements runtime.ProfileSessionHook: a rename holds
// the list_changed delivery until its session rewrite has run, so the re-list it
// triggers follows the rename instead of clearing the old-name selection.
func (p *MCPProxyServer) HoldProfileNotifications() (release func()) {
	if p.profileNotifier == nil {
		return func() {}
	}
	return p.profileNotifier.hold()
}

// ProfileRenamed implements runtime.ProfileSessionHook: the stored set_profile
// selection of every session that had selected the old name follows the rename.
func (p *MCPProxyServer) ProfileRenamed(from, to string) {
	if p.sessionStore != nil {
		p.sessionStore.RenameSelection(from, to)
	}
}

// ProfileTokensMoved implements runtime.ProfileSessionHook: the sessions of a
// token whose pin a delete-with-reassign moved get the FR-026 treatment (their
// stored selection is cleared and each is re-listed).
func (p *MCPProxyServer) ProfileTokensMoved(tokenNames []string) {
	for _, name := range tokenNames {
		p.NotifyBindingChanged(name)
	}
}

// ProfileDeleted implements runtime.ProfileSessionHook: a selection of a
// deleted profile is cleared (the session falls back to its base).
func (p *MCPProxyServer) ProfileDeleted(name string) {
	if p.sessionStore != nil {
		p.sessionStore.ClearSelection(name)
	}
}
