package upstream

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Spec 116 PR3 (#1563): generation-bound registry mutations.
//
// The config commit is the only writer of the manager's desired view: at
// commit step 7 AdvanceGenerations records, for every configured server, the
// commit generation the live configuration holds it at (and a tombstone for a
// removed one), unregisters every instance that must not survive the commit
// and restamps the rest. Every asynchronous actor that changes the registry or
// connects an instance afterwards (a post-commit worker, a supervisor action,
// RestartServer, ForceReconnectAll, Docker recovery) carries the generation it
// was scheduled for, and an action on an EXISTING instance also carries that
// instance (its InstanceID or pointer). The *At methods re-check both under
// the manager's own locks, in the same critical section as the change, and
// return ErrStaleGeneration without acting when a newer commit (or a
// same-generation replacement) superseded them.
//
// Lock order (plan.md §Lock order): captureMu (L3, W for every registry
// change) → mu (L4) → limiterPubMu (L4', taken hand over hand before mu is
// released, held across the limiter publication) → limiter.Registry.mu
// (leaf). None is held while connecting or disconnecting an instance, or
// while calling back into the runtime.
//
// Limiter publication is part of the bound action (Sol round 2): a registry
// mutation publishes the limits it decided before any later registry
// mutation can publish, so an action that validated generation G and then
// lost the CPU after the unlock cannot retire, or republish G's limits over,
// what a newer commit published.

// ErrStaleGeneration is returned by the *At methods when the action was bound
// to a generation, or an instance, that is no longer the server's current
// one. It is not a failure: the caller logs it at debug level and does
// nothing.
var ErrStaleGeneration = commitview.ErrStaleGeneration

// desiredGen is the manager's record of one server name's committed state.
// present=false is a tombstone left by a removal (manager memory only); a
// later create overwrites it.
type desiredGen struct {
	gen     int64
	present bool
}

// GenerationChange is one server's state after a config commit, as commit
// step 7 hands it to AdvanceGenerations.
type GenerationChange struct {
	Name string
	// Gen is the server's commit generation in the published snapshot (0 when
	// the server was removed).
	Gen int64
	// Present is false when the commit removed the server.
	Present bool
	// Reconnect is true when the commit changed the connection settings,
	// Enabled or Quarantined (configsvc.ServerChange.Reconnect).
	Reconnect bool
	// Replaced is true for a remove and re-create in one commit (review
	// R8.2): the old instance never survives, even for an equal entry.
	Replaced bool
	// Config is the committed entry (nil when removed).
	Config *config.ServerConfig
}

// AdvanceResult reports what AdvanceGenerations did.
type AdvanceResult struct {
	// Unregistered are the instances removed from the registry. The caller
	// disconnects them after it released its own locks (they no longer
	// connect: managed.ErrInstanceUnregistered).
	Unregistered []*managed.Client
	// Restamped names the retained instances that took the committed config
	// and generation in place (no reconnect).
	Restamped []string
}

// AdvanceGenerations applies one config commit to the registry (commit step
// 7, plan.md §Commit pipeline). changes must list EVERY server of the
// published configuration (unchanged ones too, so the desired view is complete
// after the boot commit) plus every server the commit removed. For each name:
//
//   - removed: the instance is unregistered and the name tombstoned;
//   - replaced, or a connection/Enabled/Quarantined change whose generation
//     moved: the instance is unregistered (the commit's worker installs the
//     new one);
//   - otherwise a registered instance behind the committed generation is
//     restamped with the committed config (SetConfigAt), or unregistered if it
//     does not run the committed connection settings.
//
// It takes captureMu for writing and then mu (L3 → L4); the caller holds no
// approval lock (rule 2). Limiter updates run after both are released. It
// never connects or disconnects.
func (m *Manager) AdvanceGenerations(changes []GenerationChange) AdvanceResult {
	var res AdvanceResult
	if len(changes) == 0 {
		return res
	}
	var limiterApply []*config.ServerConfig
	var limiterRetire []string

	m.captureMu.Lock()
	m.mu.Lock()
	for _, ch := range changes {
		if ch.Name == "" {
			continue
		}
		if !ch.Present {
			m.setDesiredLocked(ch.Name, desiredGen{present: false})
			if old := m.unregisterLocked(ch.Name); old != nil {
				res.Unregistered = append(res.Unregistered, old)
			}
			limiterRetire = append(limiterRetire, ch.Name)
			continue
		}
		prev, had := m.desired[ch.Name]
		moved := !had || !prev.present || prev.gen != ch.Gen
		m.setDesiredLocked(ch.Name, desiredGen{gen: ch.Gen, present: true})
		client, registered := m.clients[ch.Name]
		if !registered || client == nil {
			continue
		}
		switch {
		case ch.Replaced || (moved && ch.Reconnect),
			client.Generation() != ch.Gen && ch.Config != nil && !config.ConnectionEquivalent(client.GetConfig(), ch.Config):
			if old := m.unregisterLocked(ch.Name); old != nil {
				res.Unregistered = append(res.Unregistered, old)
			}
			if ch.Config != nil {
				limiterApply = append(limiterApply, ch.Config)
			}
		case ch.Config != nil && client.Generation() < ch.Gen:
			if client.SetConfigAt(ch.Config, ch.Gen) {
				res.Restamped = append(res.Restamped, ch.Name)
				limiterApply = append(limiterApply, ch.Config)
			}
		}
	}
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()

	for _, name := range limiterRetire {
		m.runLimiterPublishHook(name)
		m.retireServerConcurrency(name)
	}
	for _, cfg := range limiterApply {
		m.runLimiterPublishHook(cfg.Name)
		m.applyServerConcurrency(cfg)
	}
	m.limiterPubMu.Unlock()
	return res
}

// DesiredGeneration reports the committed generation the manager holds for
// name and whether the name is present (not removed). Test and diagnostics
// accessor.
func (m *Manager) DesiredGeneration(name string) (gen int64, present bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.desired[name]
	return d.gen, ok && d.present
}

// setDesiredLocked records name's committed state. Caller holds captureMu W +
// mu W. (The map is created lazily for Managers built as struct literals in
// tests.)
func (m *Manager) setDesiredLocked(name string, d desiredGen) {
	if m.desired == nil {
		m.desired = make(map[string]desiredGen)
	}
	m.desired[name] = d
}

// checkDesiredLocked is the generation check of every bound action: name must
// be present in the desired view at exactly gen. Caller holds mu (R or W).
func (m *Manager) checkDesiredLocked(name string, gen int64) error {
	d, ok := m.desired[name]
	if !ok || !d.present || d.gen != gen {
		return ErrStaleGeneration
	}
	return nil
}

// unregisterLocked is the ONLY code that removes an instance from m.clients
// (FR-020a, review R5.4). It runs under captureMu W + mu W, drops the
// server's call-outcome history in the same critical section (Spec 113-d
// FR-066), marks the instance unregistered so it never connects again, and
// returns it for an asynchronous Disconnect by the caller. Spec 116 PR4 adds
// the retirement barrier (Retire) here.
//
// The call-outcome history is dropped even when no instance is registered:
// a removed or replaced server must not carry its failure history into
// whatever reuses the name, and a completion recorded after this section
// fails the registered check.
func (m *Manager) unregisterLocked(name string) *managed.Client {
	old, ok := m.clients[name]
	statsName := name
	if ok {
		delete(m.clients, name)
		if old != nil {
			if cfg := old.GetConfig(); cfg != nil && cfg.Name != "" {
				statsName = cfg.Name
			}
			old.MarkUnregistered()
		}
	}
	m.callStats.Drop(statsName)
	m.dropCallHealth(statsName)
	if !ok {
		return nil
	}
	return old
}

// installLocked is the ONLY code that adds an instance to m.clients. The
// name must not be registered (callers unregister first). Caller holds
// captureMu W + mu W.
func (m *Manager) installLocked(name string, client *managed.Client) {
	m.clients[name] = client
}

// newWiredClientLocked builds an instance for cfg stamped with gen and wires
// the manager's callbacks and admission control into it before it becomes
// reachable. Caller holds mu.
func (m *Manager) newWiredClientLocked(id string, cfg *config.ServerConfig, gen int64) (*managed.Client, error) {
	client, err := managed.NewClientAt(id, cfg, gen, m.logger, m.logConfig, m.globalConfig.Load(), m.storage, m.secretResolver)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for server %s: %w", cfg.Name, err)
	}

	// Set up notification callback for state changes
	if m.notificationMgr != nil {
		notifierCallback := StateChangeNotifier(m.notificationMgr, cfg.Name)
		existingCallback := client.StateManager.GetStateChangeCallback()
		client.StateManager.SetStateChangeCallback(func(oldState, newState types.ConnectionState, info *types.ConnectionInfo) {
			if existingCallback != nil {
				existingCallback(oldState, newState, info)
			}
			// A superseded instance's transitions (the Disconnect of an
			// instance a commit or replacement unregistered) are notified
			// under the server NAME and would overwrite the status of the
			// instance that now serves it: the replacement could show as
			// disconnected while it is connected. They are not reported.
			if client.Unregistered() {
				return
			}
			notifierCallback(oldState, newState, info)
		})
	}
	// Tool discovery callback for notifications/tools/list_changed.
	if m.toolDiscoveryCallback != nil {
		client.SetToolDiscoveryCallback(m.toolDiscoveryCallback)
	}
	// Prompts-changed callback for notifications/prompts/list_changed (F13).
	if m.promptsChangedCallback != nil {
		client.SetPromptsChangedCallback(m.promptsChangedCallback)
	}
	// Spec 093: install admission control before the client becomes
	// reachable, so no dispatch can ever see a client without its limiter.
	client.SetAdmissionControl(m.limiters, m.currentRejectObserver())
	return client, nil
}

// installAtLocked makes the registry serve cfg at gen for id: it keeps a
// registered instance that already runs the same connection settings
// (restamping it when it is behind gen), and otherwise unregisters it and
// installs a new instance. It returns the instance now registered and the
// one it unregistered (nil if none). Caller holds captureMu W + mu W and has
// already checked the generation.
func (m *Manager) installAtLocked(id string, cfg *config.ServerConfig, gen int64, unbound bool) (inst, unregistered *managed.Client, err error) {
	if existing, ok := m.clients[id]; ok && existing != nil {
		if config.ConnectionEquivalent(existing.GetConfig(), cfg) {
			m.logger.Debug("Server configuration unchanged, keeping existing client",
				zap.String("id", id),
				zap.String("name", cfg.Name),
				zap.Int64("generation", gen),
				zap.String("current_state", existing.GetState().String()))
			if gen > 0 && !unbound {
				// Only forward: an instance at this or a newer generation
				// already holds the committed config.
				existing.SetConfigAt(cfg, gen)
			} else {
				existing.SetConfig(cfg)
			}
			return existing, nil, nil
		}
		m.logger.Info("Server configuration changed, replacing existing client",
			zap.String("id", id),
			zap.String("name", cfg.Name),
			zap.Int64("generation", gen),
			zap.String("current_state", existing.GetState().String()),
			zap.Bool("is_connected", existing.IsConnected()))
		unregistered = m.unregisterLocked(id)
	}
	client, err := m.newWiredClientLocked(id, cfg, gen)
	if err != nil {
		return nil, unregistered, err
	}
	m.installLocked(id, client)
	m.logger.Info("Added upstream server configuration",
		zap.String("id", id),
		zap.String("name", cfg.Name),
		zap.Int64("generation", gen),
		zap.Uint64("instance", uint64(client.InstanceID())))
	return client, unregistered, nil
}

// AddServerConfigAt installs (or keeps) the instance serving id at commit
// generation gen without connecting it. It returns ErrStaleGeneration, and
// changes nothing, unless gen is id's current committed generation.
func (m *Manager) AddServerConfigAt(id string, serverConfig *config.ServerConfig, gen int64) error {
	_, err := m.addServerConfigAt(id, serverConfig, gen)
	return err
}

func (m *Manager) addServerConfigAt(id string, serverConfig *config.ServerConfig, gen int64) (*managed.Client, error) {
	if serverConfig == nil {
		return nil, fmt.Errorf("nil config for server %s", id)
	}
	m.captureMu.Lock()
	m.mu.Lock()
	if err := m.checkDesiredLocked(id, gen); err != nil {
		m.mu.Unlock()
		m.captureMu.Unlock()
		return nil, err
	}
	inst, old, err := m.installAtLocked(id, serverConfig, gen, false)
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()

	if err == nil {
		// Spec 093: publish this server's limits (or retire them when the
		// server is disabled/quarantined, FR-009). Off the registry locks —
		// Retire wakes queued callers — but under limiterPubMu, so a newer
		// commit cannot publish first.
		m.runLimiterPublishHook(id)
		m.applyServerConcurrency(serverConfig)
	}
	m.limiterPubMu.Unlock()
	if old != nil {
		_ = old.Disconnect()
	}
	return inst, err
}

// AddServerAt installs (or keeps) the instance serving id at generation gen
// and, when the server is enabled, connects THAT instance (never whatever the
// name resolves to later). ErrStaleGeneration when gen is not current.
func (m *Manager) AddServerAt(id string, serverConfig *config.ServerConfig, gen int64) error {
	inst, err := m.addServerConfigAt(id, serverConfig, gen)
	if err != nil {
		return err
	}
	if !serverConfig.Enabled {
		m.logger.Debug("Skipping connection for disabled server",
			zap.String("id", id),
			zap.String("name", serverConfig.Name))
		return nil
	}
	return m.connectInstance(id, inst)
}

// ConnectServerAt connects the instance serving name at generation gen.
// ErrStaleGeneration when gen is not name's committed generation, or the
// registered instance does not run it yet.
func (m *Manager) ConnectServerAt(ctx context.Context, name string, gen int64) error {
	m.mu.RLock()
	err := m.checkDesiredLocked(name, gen)
	client := m.clients[name]
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("server %s not found", name)
	}
	if gen > 0 && client.Generation() != gen {
		return ErrStaleGeneration
	}
	return m.connectInstanceCtx(ctx, name, client)
}

// DisconnectServerAt disconnects the exact instance inst serving name at
// generation gen, and returns ErrStaleGeneration without acting when the
// generation moved or another instance (a restart, ForceReconnectAll or
// Docker recovery at the same generation) now serves the name (review R5.2).
func (m *Manager) DisconnectServerAt(name string, gen int64, inst managed.InstanceID) error {
	m.mu.RLock()
	err := m.checkDesiredLocked(name, gen)
	client := m.clients[name]
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	if client == nil || client.InstanceID() != inst {
		return ErrStaleGeneration
	}
	if err := client.Disconnect(); err != nil {
		m.logger.Warn("Error during server disconnect",
			zap.String("server", name),
			zap.Error(err))
	}
	return nil
}

// RemoveServerAt unregisters and disconnects the instance serving name. With
// gen > 0 it acts only when gen is name's current committed generation (a
// disabled server's worker); with gen == 0 it acts only when the committed
// configuration no longer has the name (the supervisor's removal of a server
// the commit already tombstoned). ErrStaleGeneration otherwise.
func (m *Manager) RemoveServerAt(name string, gen int64) error {
	m.captureMu.Lock()
	m.mu.Lock()
	var err error
	if gen == 0 {
		if d, ok := m.desired[name]; !ok || d.present {
			err = ErrStaleGeneration
		}
	} else {
		err = m.checkDesiredLocked(name, gen)
	}
	var old *managed.Client
	if err == nil {
		old = m.unregisterLocked(name)
	}
	if err != nil {
		m.mu.Unlock()
		m.captureMu.Unlock()
		return err
	}
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()
	m.runLimiterPublishHook(name)
	m.retireServerConcurrency(name)
	m.limiterPubMu.Unlock()
	if old != nil {
		m.logger.Info("Removing upstream server",
			zap.String("id", name),
			zap.String("state", old.GetState().String()))
		_ = old.Disconnect()
	}
	return nil
}

// ReplaceInstanceAt replaces expected, the instance serving name at
// generation gen, with a fresh instance at the SAME generation (RestartServer,
// ForceReconnectAll, Docker recovery). It returns ErrStaleGeneration, and
// changes nothing, when the generation moved or expected is no longer the
// registered instance (review R4.1, R5.4). cfg defaults to expected's config.
// The old instance is unregistered (it never connects again) and disconnected
// before this returns; the new one is not connected.
func (m *Manager) ReplaceInstanceAt(name string, gen int64, expected *managed.Client, cfg *config.ServerConfig) (*managed.Client, error) {
	if expected == nil {
		return nil, fmt.Errorf("replace %s: no instance to replace", name)
	}
	if cfg == nil {
		cfg = cloneServerConfig(expected.GetConfig())
	}
	if cfg == nil {
		return nil, fmt.Errorf("replace %s: no config", name)
	}
	m.captureMu.Lock()
	m.mu.Lock()
	if err := m.checkDesiredLocked(name, gen); err != nil {
		m.mu.Unlock()
		m.captureMu.Unlock()
		return nil, err
	}
	if cur := m.clients[name]; cur != expected {
		m.mu.Unlock()
		m.captureMu.Unlock()
		return nil, ErrStaleGeneration
	}
	old := m.unregisterLocked(name)
	client, err := m.newWiredClientLocked(name, cfg, gen)
	if err == nil {
		m.installLocked(name, client)
	}
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()

	m.runLimiterPublishHook(name)
	if err == nil {
		m.applyServerConcurrency(cfg)
	} else {
		// The name stays unregistered; the next commit or supervisor pass
		// installs it again.
		m.retireServerConcurrency(name)
	}
	m.limiterPubMu.Unlock()
	if old != nil {
		_ = old.Disconnect()
	}
	if err != nil {
		return nil, err
	}
	m.logger.Info("Replaced upstream server instance",
		zap.String("server", name),
		zap.Int64("generation", gen),
		zap.Uint64("old_instance", uint64(expected.InstanceID())),
		zap.Uint64("new_instance", uint64(client.InstanceID())))
	return client, nil
}

// ConnectInstance connects inst, an instance a bound call (AddServerAt,
// ReplaceInstanceAt) returned, with the server's resolved init timeout. It
// never resolves the name again. An instance that has since been unregistered
// refuses to connect, which is reported as success (nothing failed).
func (m *Manager) ConnectInstance(name string, inst *managed.Client) error {
	return m.connectInstance(name, inst)
}

func (m *Manager) connectInstance(name string, inst *managed.Client) error {
	return m.connectInstanceCtx(context.Background(), name, inst)
}

// connectInstanceCtx is the connect step shared by every bound path. A
// connected instance, a connect already in flight, a dial superseded by a
// disconnect and an unregistered instance are not failures; an OAuth
// requirement is not either (the connection completes after login).
func (m *Manager) connectInstanceCtx(parent context.Context, name string, inst *managed.Client) error {
	if inst == nil {
		return fmt.Errorf("server %s not found", name)
	}
	cfg := inst.GetConfig()
	serverName := name
	if cfg != nil && cfg.Name != "" {
		serverName = cfg.Name
	}
	if inst.IsConnected() {
		m.logger.Debug("Server is already connected, skipping connection attempt",
			zap.String("id", name),
			zap.String("name", serverName))
		return nil
	}
	// The resolved init_timeout (MCP-3322) bounds the handshake.
	ctx, cancel := context.WithTimeout(parent, m.resolveConnectTimeout(cfg, inst.DependsOnDocker()))
	defer cancel()
	err := inst.Connect(ctx)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, managed.ErrConnectAlreadyActive):
		m.logger.Debug("Connect already in progress or established, not starting another",
			zap.String("id", name),
			zap.String("name", serverName),
			zap.String("state", inst.GetState().String()))
		return nil
	case errors.Is(err, managed.ErrConnectAborted):
		m.logger.Debug("Connect superseded by a concurrent disconnect",
			zap.String("id", name),
			zap.String("name", serverName))
		return nil
	case errors.Is(err, managed.ErrInstanceUnregistered):
		m.logger.Debug("Connect skipped: the instance was superseded by a newer commit or replacement",
			zap.String("id", name),
			zap.String("name", serverName),
			zap.Uint64("instance", uint64(inst.InstanceID())))
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "OAuth") || strings.Contains(errStr, "oauth") || strings.Contains(errStr, "authorization required") {
		m.logger.Info("Server requires OAuth authorization - connection will complete after OAuth login",
			zap.String("id", name),
			zap.String("name", serverName),
			zap.Error(err))
		return nil
	}
	return fmt.Errorf("failed to connect to server %s: %w", serverName, err)
}

// Legacy, generation-unbound registry API. It is kept for tests and the
// CLI-only code paths that construct a Manager without a config commit. A
// registration made through it for a name no commit recorded is recorded at
// generation 0 (so a bound call at generation 0 matches it); for a committed
// name it installs at the committed generation and never moves it. The daemon
// never uses it:
// scripts/check-upstream-write-sites.sh forbids these calls in
// internal/runtime and internal/server, and inside this package outside the
// *At implementations.

// AddServerConfig adds a server configuration without connecting (unbound).
func (m *Manager) AddServerConfig(id string, serverConfig *config.ServerConfig) error {
	_, err := m.addServerConfigUnbound(id, serverConfig)
	return err
}

func (m *Manager) addServerConfigUnbound(id string, serverConfig *config.ServerConfig) (*managed.Client, error) {
	if serverConfig == nil {
		return nil, fmt.Errorf("nil config for server %s", id)
	}
	m.captureMu.Lock()
	m.mu.Lock()
	// An unbound call acts as "the current config": for a name a commit has
	// recorded it uses that committed generation (and never moves it); for
	// any other name it records generation 0.
	gen := int64(0)
	if d, ok := m.desired[id]; ok && d.present {
		gen = d.gen
	} else {
		m.setDesiredLocked(id, desiredGen{gen: 0, present: true})
	}
	inst, old, err := m.installAtLocked(id, serverConfig, gen, true)
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()
	if err == nil {
		m.runLimiterPublishHook(id)
		m.applyServerConcurrency(serverConfig)
	}
	m.limiterPubMu.Unlock()
	if old != nil {
		_ = old.Disconnect()
	}
	return inst, err
}

// AddServer adds a server and connects it (unbound legacy method).
func (m *Manager) AddServer(id string, serverConfig *config.ServerConfig) error {
	inst, err := m.addServerConfigUnbound(id, serverConfig)
	if err != nil {
		return err
	}
	if !serverConfig.Enabled {
		return nil
	}
	return m.connectInstance(id, inst)
}

// RemoveServer removes an upstream server (unbound legacy method).
func (m *Manager) RemoveServer(id string) {
	m.captureMu.Lock()
	m.mu.Lock()
	// A name a commit recorded keeps its committed state: the commit, not an
	// unbound call, owns it.
	if d, ok := m.desired[id]; ok && d.gen == 0 {
		delete(m.desired, id)
	}
	old := m.unregisterLocked(id)
	m.limiterPubMu.Lock()
	m.mu.Unlock()
	m.captureMu.Unlock()

	// Spec 093 FR-009: tombstone the limiter first so queued calls fail
	// immediately with the server-unavailable semantics.
	name := id
	if old != nil {
		if cfg := old.GetConfig(); cfg != nil && cfg.Name != "" {
			name = cfg.Name
		}
	}
	m.runLimiterPublishHook(name)
	m.retireServerConcurrency(name)
	m.limiterPubMu.Unlock()
	if old != nil {
		m.logger.Info("Removing upstream server",
			zap.String("id", id),
			zap.String("state", old.GetState().String()))
		_ = old.Disconnect()
	}
}

// runLimiterPublishHook runs the test seam, if installed.
func (m *Manager) runLimiterPublishHook(name string) {
	if h := m.limiterPublishHook.Load(); h != nil {
		(*h)(name)
	}
}
