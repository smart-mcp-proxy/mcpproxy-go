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
}

type typedOverride[T any] struct {
	field   Field[T]
	src     OverrideSource
	process T // the value this process runs with
	loaded  T // the file's value when the override was applied
}

func (o typedOverride[T]) name() string           { return o.field.Name }
func (o typedOverride[T]) source() OverrideSource { return o.src }

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

var (
	processOverridesMu sync.RWMutex
	// processOverrides is keyed by field name; a later override of the same
	// field (a reload re-applying env, a flag applied after env) replaces the
	// earlier one — one process value per field.
	processOverrides = map[string]processOverride{}
)

// OverrideForProcess sets field f on cfg to value for THIS PROCESS ONLY and
// records it, so PersistableConfig (and therefore SaveConfig) writes the file's
// value back as long as the effective value still equals the override.
func OverrideForProcess[T any](cfg *Config, f Field[T], source OverrideSource, value T) {
	if cfg == nil {
		return
	}
	loaded := f.Get(cfg)
	f.Set(cfg, value)

	processOverridesMu.Lock()
	processOverrides[f.Name] = typedOverride[T]{field: f, src: source, process: value, loaded: loaded}
	processOverridesMu.Unlock()
}

// clearProcessOverrides drops every override recorded from source. The loader
// calls it for OverrideSourceEnv before re-applying the environment on each
// load, so a reload reflects the variables set NOW; flag overrides, applied
// once at startup, survive reloads.
func clearProcessOverrides(source OverrideSource) {
	processOverridesMu.Lock()
	defer processOverridesMu.Unlock()
	for name, o := range processOverrides {
		if o.source() == source {
			delete(processOverrides, name)
		}
	}
}

// ResetProcessOverrides forgets every recorded override. For tests.
func ResetProcessOverrides() {
	processOverridesMu.Lock()
	processOverrides = map[string]processOverride{}
	processOverridesMu.Unlock()
}

// ProcessOverrideFields lists the names of the fields currently overridden for
// this process, sorted, for diagnostics and logging.
func ProcessOverrideFields() []string {
	processOverridesMu.RLock()
	defer processOverridesMu.RUnlock()
	names := make([]string, 0, len(processOverrides))
	for name := range processOverrides {
		names = append(names, name)
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
func PersistableConfig(effective *Config, path string) *Config {
	if effective == nil {
		return nil
	}
	processOverridesMu.RLock()
	overrides := make([]processOverride, 0, len(processOverrides))
	for _, o := range processOverrides {
		overrides = append(overrides, o)
	}
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
		o.restore(&out, base)
	}
	return &out
}
