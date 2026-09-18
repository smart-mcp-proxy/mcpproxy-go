package config

import (
	"reflect"
	"sort"
	"sync"
)

// Process-only overrides: the persisted-vs-effective split.
//
// The config a process runs with is the file PLUS a layer of one-off choices —
// `serve` CLI flags (--listen, --read-only, --tool-response-mode, ...), the
// MCPPROXY_* environment variables the loader applies, and the MCPPROXY_API_KEY
// that Validate copies into api_key. That effective config is the only one the
// daemon holds: the runtime, the telemetry service and every API handler read
// and — crucially — SAVE it. Before this existed, any persist path (first-run
// anonymous_id generation, a server enable from the Web UI, the startup-outcome
// stamp) wrote the overrides into mcp_config.json, and the next unflagged start
// — or the tray-launched core — inherited a choice that was meant for one run
// (`--listen :0` used to leave `"listen": ":0"` behind and the core silently
// booted in stdio mode).
//
// The registry below records, per overridden field, the value this process was
// given and the value the file held when it was applied. PersistableConfig then
// answers "what should go to disk": for every recorded field whose EFFECTIVE
// value still equals the override, the file's value is written back; a field
// that has since been edited (the Settings page changing listen, the tray
// picking an alternate port) no longer matches the override and is persisted
// as the edit it is. That is what keeps the API's legitimate edits of the very
// same fields working — a blanket "always restore the file value" would have
// thrown those edits away.
//
// SaveConfig applies the split centrally so every persist path — the runtime,
// telemetry, the server edition's admin handlers, the CLI subcommands that
// load-modify-save — is covered without each having to remember. Callers that
// need to know the exact bytes going to disk (the runtime's config-watcher
// self-write markers) call PersistableConfig themselves first; the mapping is
// idempotent, so SaveConfig re-applying it is harmless.

// OverrideSource says where a process-only override came from.
type OverrideSource string

const (
	// OverrideSourceFlag is a `serve` command-line flag.
	OverrideSourceFlag OverrideSource = "flag"
	// OverrideSourceEnv is a MCPPROXY_* environment variable, including the
	// MCPPROXY_API_KEY that Validate folds into api_key.
	OverrideSourceEnv OverrideSource = "env"
)

// Field names a configuration value a process-only override can shadow. Set
// copies nested structs before writing (copy-on-write) so a PersistableConfig
// result never writes through a pointer it shares with the effective config.
type Field[T any] struct {
	Name string
	Get  func(*Config) T
	Set  func(*Config, T)
}

// The overridable fields. Keep in lockstep with the overrides applied in
// cmd/mcpproxy (serve flags), applyTLSEnvOverrides (env) and Validate (env API
// key): an override applied without going through OverrideForProcess is
// persisted by every save path, which is the bug this file exists to close.
var (
	FieldListen                   = scalar("listen", func(c *Config) *string { return &c.Listen })
	FieldDataDir                  = scalar("data_dir", func(c *Config) *string { return &c.DataDir })
	FieldAPIKey                   = scalar("api_key", func(c *Config) *string { return &c.APIKey })
	FieldTrayEndpoint             = scalar("tray_endpoint", func(c *Config) *string { return &c.TrayEndpoint })
	FieldEnableSocket             = scalar("enable_socket", func(c *Config) *bool { return &c.EnableSocket })
	FieldToolResponseLimit        = scalar("tool_response_limit", func(c *Config) *int { return &c.ToolResponseLimit })
	FieldToolResponseMode         = scalar("tool_response_mode", func(c *Config) *string { return &c.ToolResponseMode })
	FieldDirectToolResponseMode   = scalar("direct_tool_response_mode", func(c *Config) *string { return &c.DirectToolResponseMode })
	FieldDebugSearch              = scalar("debug_search", func(c *Config) *bool { return &c.DebugSearch })
	FieldRequireMCPAuth           = scalar("require_mcp_auth", func(c *Config) *bool { return &c.RequireMCPAuth })
	FieldReadOnlyMode             = scalar("read_only_mode", func(c *Config) *bool { return &c.ReadOnlyMode })
	FieldDisableManagement        = scalar("disable_management", func(c *Config) *bool { return &c.DisableManagement })
	FieldAllowServerAdd           = scalar("allow_server_add", func(c *Config) *bool { return &c.AllowServerAdd })
	FieldAllowServerRemove        = scalar("allow_server_remove", func(c *Config) *bool { return &c.AllowServerRemove })
	FieldEnablePrompts            = scalar("enable_prompts", func(c *Config) *bool { return &c.EnablePrompts })
	FieldAggregateUpstreamPrompts = scalar("aggregate_upstream_prompts", func(c *Config) *bool { return &c.AggregateUpstreamPrompts })
	FieldTrustedHosts             = scalar("trusted_hosts", func(c *Config) *[]string { return &c.TrustedHosts })
	FieldMaxConcurrentRequests    = scalar("max_concurrent_requests", func(c *Config) **int { return &c.MaxConcurrentRequests })
	FieldQueueSize                = scalar("queue_size", func(c *Config) **int { return &c.QueueSize })
	FieldQueueTimeout             = scalar("queue_timeout", func(c *Config) **Duration { return &c.QueueTimeout })
	FieldHTTPReadTimeout          = scalar("http_read_timeout", func(c *Config) **Duration { return &c.HTTPReadTimeout })
	FieldHTTPWriteTimeout         = scalar("http_write_timeout", func(c *Config) **Duration { return &c.HTTPWriteTimeout })
	FieldHTTPIdleTimeout          = scalar("http_idle_timeout", func(c *Config) **Duration { return &c.HTTPIdleTimeout })

	FieldLogLevel = Field[string]{
		Name: "logging.level",
		Get:  func(c *Config) string { return loggingOf(c).Level },
		Set:  func(c *Config, v string) { l := cowLogging(c); l.Level = v },
	}
	FieldLogEnableFile = Field[bool]{
		Name: "logging.enable_file",
		Get:  func(c *Config) bool { return loggingOf(c).EnableFile },
		Set:  func(c *Config, v bool) { l := cowLogging(c); l.EnableFile = v },
	}
	FieldLogDir = Field[string]{
		Name: "logging.log_dir",
		Get:  func(c *Config) string { return loggingOf(c).LogDir },
		Set:  func(c *Config, v string) { l := cowLogging(c); l.LogDir = v },
	}
	FieldTLSEnabled = Field[bool]{
		Name: "tls.enabled",
		Get:  func(c *Config) bool { return tlsOf(c).Enabled },
		Set:  func(c *Config, v bool) { t := cowTLS(c); t.Enabled = v },
	}
	FieldTLSRequireClientCert = Field[bool]{
		Name: "tls.require_client_cert",
		Get:  func(c *Config) bool { return tlsOf(c).RequireClientCert },
		Set:  func(c *Config, v bool) { t := cowTLS(c); t.RequireClientCert = v },
	}
	FieldTLSCertsDir = Field[string]{
		Name: "tls.certs_dir",
		Get:  func(c *Config) string { return tlsOf(c).CertsDir },
		Set:  func(c *Config, v string) { t := cowTLS(c); t.CertsDir = v },
	}
	FieldTPABundlePath = Field[string]{
		Name: "security.tpa_bundle_path",
		Get:  func(c *Config) string { return securityOf(c).TPABundlePath },
		Set:  func(c *Config, v string) { s := cowSecurity(c); s.TPABundlePath = v },
	}
	FieldAutoBaselineScan = Field[*bool]{
		Name: "security.auto_baseline_scan",
		Get:  func(c *Config) *bool { return securityOf(c).AutoBaselineScan },
		Set: func(c *Config, v *bool) {
			if c.Security == nil && v == nil {
				return // nothing to unset; do not materialize an empty block
			}
			s := cowSecurity(c)
			s.AutoBaselineScan = v
		},
	}
)

// scalar builds a Field for a top-level value addressed by pointer.
func scalar[T any](name string, ptr func(*Config) *T) Field[T] {
	return Field[T]{
		Name: name,
		Get:  func(c *Config) T { return *ptr(c) },
		Set:  func(c *Config, v T) { *ptr(c) = v },
	}
}

// loggingOf/tlsOf/securityOf read a nested block, treating a missing one as
// its zero value; cowLogging/cowTLS/cowSecurity replace the block with a copy
// before a write so the pointer shared with the effective config is untouched.
func loggingOf(c *Config) LogConfig {
	if c.Logging == nil {
		return LogConfig{}
	}
	return *c.Logging
}

func cowLogging(c *Config) *LogConfig {
	l := loggingOf(c)
	c.Logging = &l
	return c.Logging
}

func tlsOf(c *Config) TLSConfig {
	if c.TLS == nil {
		return TLSConfig{}
	}
	return *c.TLS
}

func cowTLS(c *Config) *TLSConfig {
	t := tlsOf(c)
	c.TLS = &t
	return c.TLS
}

func securityOf(c *Config) SecurityConfig {
	if c.Security == nil {
		return SecurityConfig{}
	}
	return *c.Security
}

func cowSecurity(c *Config) *SecurityConfig {
	s := securityOf(c)
	c.Security = &s
	return c.Security
}

// processOverride is one recorded override.
type processOverride interface {
	name() string
	source() OverrideSource
	// restore writes the persisted value of the field into out when out still
	// carries the process value. base is the file as it stands now (nil when
	// unreadable, in which case the value the file held at override time is
	// used).
	restore(out, base *Config)
	// loadedValue is the file value recorded when the override was applied.
	loadedValue() any
	// supersededBy reports whether live no longer carries the process value.
	supersededBy(live *Config) bool
	// movedBetween reports whether the field differs between base and next.
	movedBetween(base, next *Config) bool
	// reapply layers the process value back onto a freshly loaded cfg and
	// returns the entry with its recorded file value refreshed (loaded is
	// what cfg held before, or fileValue when the caller knows better).
	reapply(cfg *Config, fileValue any, useFileValue bool) processOverride
}

type typedOverride[T any] struct {
	field   Field[T]
	src     OverrideSource
	process T // the value this process runs with
	loaded  T // the file's value when the override was applied
}

func (o typedOverride[T]) name() string           { return o.field.Name }
func (o typedOverride[T]) source() OverrideSource { return o.src }
func (o typedOverride[T]) loadedValue() any       { return o.loaded }

func (o typedOverride[T]) restore(out, base *Config) {
	if !reflect.DeepEqual(o.field.Get(out), o.process) {
		return // edited since the override was applied: a real change, persist it
	}
	fileValue := o.loaded
	if base != nil {
		fileValue = o.field.Get(base)
	}
	o.field.Set(out, fileValue)
}

// supersededBy reports whether live no longer carries the process value.
func (o typedOverride[T]) supersededBy(live *Config) bool {
	return !reflect.DeepEqual(o.field.Get(live), o.process)
}

// movedBetween reports whether the field differs between base and next.
func (o typedOverride[T]) movedBetween(base, next *Config) bool {
	return !reflect.DeepEqual(o.field.Get(base), o.field.Get(next))
}

func (o typedOverride[T]) reapply(cfg *Config, fileValue any, useFileValue bool) processOverride {
	o.loaded = o.field.Get(cfg)
	if v, ok := fileValue.(T); ok && useFileValue {
		o.loaded = v
	}
	o.field.Set(cfg, o.process)
	return o
}

// overrideKey identifies one registry entry: the same field may be overridden
// by env AND by a flag (the flag, applied later, wins in memory), and both
// records have to survive — a reload rebuilds the env set, and dropping the
// flag record with it would turn the flag's value into "an edit" on the next
// save.
type overrideKey struct {
	field  string
	source OverrideSource
}

var (
	processOverridesMu sync.RWMutex
	processOverrides   = map[overrideKey]processOverride{}
)

// OverrideForProcess sets field f on cfg to value for THIS PROCESS ONLY and
// records it, so PersistableConfig (and therefore SaveConfig) writes the file's
// value back as long as the effective value still equals the override.
func OverrideForProcess[T any](cfg *Config, f Field[T], source OverrideSource, value T) {
	if cfg == nil {
		return
	}
	entry := newOverride(cfg, f, source, value)
	f.Set(cfg, value)

	processOverridesMu.Lock()
	processOverrides[overrideKey{f.Name, source}] = entry
	processOverridesMu.Unlock()
}

// newOverride builds the record for applying value to f WITHOUT setting it.
// The recorded file value is what cfg holds now — unless this is a flag
// layered over an env override of the same field, whose record already
// knows the real file value.
//
// A repeated registration of the same override (loadConfig and runServer both
// apply --tool-response-limit; Validate runs twice) finds the previous
// override already in place, so the file value is inherited from the
// existing record rather than read off cfg.
func newOverride[T any](cfg *Config, f Field[T], source OverrideSource, value T) typedOverride[T] {
	loaded := f.Get(cfg)
	processOverridesMu.RLock()
	prev, hasPrev := processOverrides[overrideKey{f.Name, source}]
	env, hasEnv := processOverrides[overrideKey{f.Name, OverrideSourceEnv}]
	processOverridesMu.RUnlock()
	if hasPrev {
		if p, ok := prev.(typedOverride[T]); ok && reflect.DeepEqual(loaded, p.process) {
			loaded = p.loaded
		}
	}
	if source == OverrideSourceFlag && hasEnv {
		if v, ok := env.loadedValue().(T); ok {
			loaded = v
		}
	}
	return typedOverride[T]{field: f, src: source, process: value, loaded: loaded}
}

// envOverrideBatch collects the env overrides of one load and commits them in
// ONE registry update. The loader rebuilds the env set on every load (so a
// reload reflects the variables set now); clearing and re-adding under
// separate locks would leave a window in which a save on another goroutine —
// telemetry, the runtime — sees no env entries at all and persists them.
type envOverrideBatch struct {
	managed []string          // every field the loader consults, set or not
	entries []processOverride // the ones that are set
}

// consider applies an env override for f when present and, either way, marks
// f as env-managed so a stale entry for it is dropped at commit.
func envOverride[T any](b *envOverrideBatch, cfg *Config, f Field[T], present bool, value T) {
	b.managed = append(b.managed, f.Name)
	if !present {
		return
	}
	b.entries = append(b.entries, newOverride(cfg, f, OverrideSourceEnv, value))
	f.Set(cfg, value)
}

// commit atomically replaces the env entries of every managed field with the
// batch. Entries of other sources (flags) and env entries the loader does not
// manage (api_key, recorded by Validate/EnsureAPIKey) are untouched.
func (b *envOverrideBatch) commit() {
	processOverridesMu.Lock()
	defer processOverridesMu.Unlock()
	for _, name := range b.managed {
		delete(processOverrides, overrideKey{name, OverrideSourceEnv})
	}
	for _, e := range b.entries {
		processOverrides[overrideKey{e.name(), OverrideSourceEnv}] = e
	}
}

// ReapplyFlagOverrides layers every flag-sourced override back onto cfg — a
// config freshly loaded from the file, on which the loader has already
// re-applied the env overrides but knows nothing about the serve flags — and
// refreshes each record's file value. Without it a hot reload after an
// external edit silently switched `--read-only` (or any other flag) off.
//
// live is the config this process was running before the reload. A flag the
// live config no longer carries was superseded by an API edit (which is on
// disk by now) and is not resurrected over that edit.
func ReapplyFlagOverrides(cfg, live *Config) {
	if cfg == nil {
		return
	}
	processOverridesMu.Lock()
	defer processOverridesMu.Unlock()
	for key, o := range processOverrides {
		if key.source != OverrideSourceFlag {
			continue
		}
		if live != nil && o.supersededBy(live) {
			// Superseded by an API edit that is on disk by now: the file
			// value stands. The record is KEPT — a stale config still
			// carrying the flag value (telemetry's, an in-flight save's)
			// must go on restoring the file value rather than persist it.
			continue
		}
		// Over an env override of the same field the config already carries
		// the env value; the env record knows what the file said.
		env, hasEnv := processOverrides[overrideKey{key.field, OverrideSourceEnv}]
		var fileValue any
		if hasEnv {
			fileValue = env.loadedValue()
		}
		processOverrides[key] = o.reapply(cfg, fileValue, hasEnv)
	}
}

// effectiveOverridesLocked returns the one override that is in force per
// field: a flag shadows an env override of the same field (the flag is
// applied after the env value, so it is what the process actually runs with).
// Only the winner may decide whether the field was edited — the shadowed env
// record would otherwise intercept an API edit that happens to equal the env
// value. Caller must hold processOverridesMu (read or write).
func effectiveOverridesLocked() []processOverride {
	winners := make(map[string]processOverride, len(processOverrides))
	for key, o := range processOverrides {
		if prev, ok := winners[key.field]; ok && prev.source() == OverrideSourceFlag {
			continue
		}
		winners[key.field] = o
	}
	out := make([]processOverride, 0, len(winners))
	for _, o := range winners {
		out = append(out, o)
	}
	return out
}

// ResetProcessOverrides forgets every recorded override. For tests.
func ResetProcessOverrides() {
	processOverridesMu.Lock()
	processOverrides = map[overrideKey]processOverride{}
	processOverridesMu.Unlock()
}

// ProcessOverrideFields lists the names of the fields currently overridden for
// this process, sorted and de-duplicated, for diagnostics and logging.
func ProcessOverrideFields() []string {
	processOverridesMu.RLock()
	defer processOverridesMu.RUnlock()
	seen := make(map[string]struct{}, len(processOverrides))
	names := make([]string, 0, len(processOverrides))
	for key := range processOverrides {
		if _, dup := seen[key.field]; dup {
			continue
		}
		seen[key.field] = struct{}{}
		names = append(names, key.field)
	}
	sort.Strings(names)
	return names
}

// PersistableConfig returns the config that should go to disk at path for the
// given effective config: a shallow copy in which every field that still
// carries its process-only override is replaced by the value the file at path
// holds now (or held when the override was applied, if the file cannot be read).
// Fields that were edited since keep the edit. With no overrides recorded the
// effective config is returned as is.
//
// The copy shares Servers, Registries and every nested block it does not touch
// with effective; the ones it restores are copied first. Callers must not
// mutate the shared structures.
//
// A save that carries an API edit uses PersistableConfigWithEdits instead, so
// the edited fields are persisted whatever they equal.
func PersistableConfig(effective *Config, path string) *Config {
	return PersistableConfigWithEdits(effective, nil, path)
}

// PersistableConfigWithEdits is PersistableConfig for the save that persists
// an API edit: mergeBase is the config the edit was merged onto (the desired
// config for PUT/PATCH /api/v1/config), and every overridden field that
// MOVED between mergeBase and effective is the caller's edit — persisted as
// is, whatever it moved to. Moving listen from the file's address to the
// flag's own address is the operator making that address permanent
// (base != next == override is distinguishable, unlike a plain round trip);
// moving it back to the file's value is an edit too. A field that merely
// round-tripped a value mergeBase already held — the file's listen after a
// disk reload, say — keeps restoring the file value, whatever it equals.
//
// The override records are never removed by an edit. The one save that
// carries the edit ignores them; every other save — a concurrent telemetry
// write of the still-live config, the next server enable — keeps restoring
// the CURRENT file value, which after the edit's save is the edit itself.
// That is what makes an edit of api_key under MCPPROXY_API_KEY safe: there
// is no window in which a stale config carrying the env secret is
// unprotected.
//
// Known limitation: an edit that sets an overridden field to exactly the
// override's value while mergeBase already holds that value is
// indistinguishable from a round trip and is not persisted — unreachable
// from the Web UI, which already shows the override as the current value.
func PersistableConfigWithEdits(effective, mergeBase *Config, path string) *Config {
	if effective == nil {
		return nil
	}
	processOverridesMu.RLock()
	overrides := effectiveOverridesLocked()
	processOverridesMu.RUnlock()
	if len(overrides) == 0 {
		return effective
	}

	var base *Config
	if path != "" {
		if onDisk, err := ReadFile(path); err == nil {
			base = onDisk
		}
	}

	out := *effective
	for _, o := range overrides {
		if mergeBase != nil && o.movedBetween(mergeBase, effective) {
			continue // the caller's edit
		}
		o.restore(&out, base)
	}
	return &out
}
