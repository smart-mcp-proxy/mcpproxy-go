// Spec 060 — declarative settings field catalogue.
// Drives the form-based Settings page. Each entry maps a dot-path into the
// mcpproxy config to a labelled control. Keeping this declarative makes the
// page auditable ("every non-deprecated option reachable") and gives every
// control a systematic data-test id.

export type ControlType =
  | 'toggle'
  | 'select'
  | 'number'
  | 'text'
  | 'textarea'
  | 'secret'
  | 'duration'
  | 'multiselect'

export interface SelectOption {
  value: string
  label: string
}

export interface DangerSpec {
  // Confirm before applying. For toggles/selects, only when the new value
  // equals confirmValue (omit to always confirm on change).
  message: string
  confirmValue?: unknown
  // 'danger' (default) = security-risky change, shown red + "sensitive" badge.
  // 'info' = a gentle "are you sure?" (e.g. opting out of telemetry) — neutral
  // styling, no sensitive badge.
  tone?: 'danger' | 'info'
}

// Extra validation applied to text/secret fields (beyond number/duration,
// which are validated from the control type). Centralised in validateField.
export type ValueKind = 'hostport' | 'bytesize' | 'cpu' | 'hostname' | 'url' | 'secretkey'

export interface SettingField {
  key: string // dot-path, e.g. "docker_isolation.enabled"
  label: string
  help?: string
  control: ControlType
  options?: SelectOption[]
  min?: number
  max?: number
  step?: number
  restart?: boolean // requires server restart to take effect
  danger?: DangerSpec
  placeholder?: string
  docs?: string // doc page path on docs.mcpproxy.app, e.g. "/features/docker-isolation"
  valueKind?: ValueKind // extra format validation for text/secret fields
  optional?: boolean // when true, an empty value is valid (skips kind validation)
  resetDefault?: string // when set, render an inline "Reset to default" button that emits this value
}

export interface SettingsAccordion {
  id: string
  title: string
  description?: string
  fields: SettingField[]
  docs?: string // doc page path on docs.mcpproxy.app
}

// Base URL for the hosted documentation; field/accordion `docs` are paths under it.
export const DOCS_BASE = 'https://docs.mcpproxy.app'

export function docsUrl(path?: string): string | undefined {
  if (!path) return undefined
  return path.startsWith('http') ? path : DOCS_BASE + path
}

// Matches Go time.Duration syntax: one or more <number><unit> segments,
// e.g. "2m", "90s", "1h30m", "500ms". Used to validate duration fields.
const DURATION_RE = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

// validateHostPort checks a Go-style listen address: "[host]:port" where the
// host part may be empty (all interfaces), an IPv4/hostname, or a bracketed
// IPv6 literal. Port must be 1–65535.
function validateHostPort(s: string): string | null {
  let host: string
  let portStr: string
  if (s.startsWith('[')) {
    const close = s.indexOf(']')
    if (close === -1) return 'Unclosed “[” in IPv6 address'
    host = s.slice(1, close)
    const rest = s.slice(close + 1)
    if (!rest.startsWith(':')) return 'Expected [ipv6]:port, e.g. [::1]:8080'
    portStr = rest.slice(1)
  } else {
    const i = s.lastIndexOf(':')
    if (i === -1) return 'Must include a port, e.g. 127.0.0.1:8080'
    host = s.slice(0, i)
    portStr = s.slice(i + 1)
  }
  if (!/^\d+$/.test(portStr)) return 'Port must be a number, e.g. 8080'
  const port = Number(portStr)
  if (port < 1 || port > 65535) return 'Port must be between 1 and 65535'
  if (host && !/^[A-Za-z0-9.\-:]+$/.test(host)) return 'Invalid host in address'
  return null
}

// validateField returns a human-readable error string for an invalid value,
// or null when the value is acceptable. Shared by the field control (to show
// the error) and the section (to block Save).
export function validateField(field: SettingField, value: unknown): string | null {
  if (field.control === 'number') {
    if (value === '' || value == null) return 'Enter a number'
    const n = Number(value)
    if (Number.isNaN(n)) return 'Must be a number'
    if (field.min != null && n < field.min) return `Must be ≥ ${field.min}`
    if (field.max != null && n > field.max) return `Must be ≤ ${field.max}`
  }
  if (field.control === 'duration') {
    const s = String(value ?? '').trim()
    // An optional duration left blank means "inherit the default" (tri-state
    // nil) and is valid; only a required duration must be non-empty.
    if (s === '') return field.optional ? null : 'Enter a duration, e.g. 2m'
    if (!DURATION_RE.test(s)) return 'Use a duration like 2m, 90s, or 1h30m'
  }
  if (field.valueKind && (field.control === 'text' || field.control === 'secret')) {
    const s = String(value ?? '').trim()
    if (s === '') return field.optional ? null : 'This field is required'
    switch (field.valueKind) {
      case 'hostport':
        return validateHostPort(s)
      case 'bytesize':
        // Docker-style size: 512m, 1g, 256k, 1073741824 (bytes), optional unit
        return /^\d+(\.\d+)?\s*([bkmgtBKMGT]i?b?)?$/.test(s) ? null : 'Use a size like 512m, 1g, or 256k'
      case 'cpu':
        return /^\d+(\.\d+)?$/.test(s) && Number(s) > 0 ? null : 'Use a positive number of CPUs, e.g. 1.0 or 0.5'
      case 'hostname':
        return /^[A-Za-z0-9.\-]+(:\d+)?(\/[^\s]*)?$/.test(s) ? null : 'Use a registry host like docker.io or ghcr.io'
      case 'url':
        return /^https?:\/\/[^\s]+$/.test(s) ? null : 'Use a URL starting with http:// or https://'
      case 'secretkey':
        // A non-empty key must be reasonably strong. Empty is handled above
        // (api_key uses optional:true → blank keeps the current key).
        return s.length >= 16 ? null : 'API key must be at least 16 characters'
    }
  }
  return null
}

// ---- Section 1: Security & Access (prioritised, security-first) ----
export const SECURITY_FIELDS: SettingField[] = [
  {
    key: 'api_key',
    label: 'API key',
    help: 'Secret that authenticates clients to the REST API and Web UI (sent as the "X-API-Key" header or "?apikey=" in the URL). Leave blank to keep the current key; ↻ generates a new one. Changing it requires a restart and re-connecting clients.',
    control: 'secret',
    valueKind: 'secretkey',
    optional: true,
    restart: true,
    placeholder: '•••••••• (unchanged — type to replace)',
  },
  {
    key: 'require_mcp_auth',
    label: 'Require API key for MCP clients',
    help: 'When on, AI clients connecting to the /mcp endpoint must send the API key above (as an "Authorization: Bearer <key>" header). Use “Connect a client” to configure this automatically. When off, any local client can connect without a key.',
    control: 'toggle',
  },
  {
    key: 'quarantine_enabled',
    docs: '/features/security-quarantine',
    label: 'Quarantine new servers & changed tools',
    help: 'Holds newly added servers and tools whose description/schema changed for your approval before agents can call them — protects against Tool Poisoning Attacks. Recommended ON.',
    control: 'toggle',
    danger: {
      confirmValue: false,
      message:
        'Disabling quarantine removes Tool Poisoning Attack protection — new servers and changed tools will run without your approval. Continue?',
    },
  },
  {
    key: 'docker_isolation.enabled',
    docs: '/features/docker-isolation',
    label: 'Run stdio servers in Docker',
    help: 'Runs command-based (stdio) MCP servers inside isolated Docker containers instead of directly on your machine. Requires Docker to be installed and running.',
    control: 'toggle',
  },
  {
    key: 'enable_code_execution',
    docs: '/features/code-execution',
    label: 'Enable code execution tool',
    help: 'Adds a sandboxed JavaScript tool agents can use to orchestrate several tool calls in one request. Off by default.',
    control: 'toggle',
  },
  {
    key: 'read_only_mode',
    label: 'Read-only mode',
    help: 'Blocks every write and destructive tool call across all servers — agents can only read. Useful for safe exploration.',
    control: 'toggle',
  },
  {
    key: 'sensitive_data_detection.enabled',
    docs: '/features/sensitive-data-detection',
    label: 'Scan for secrets in tool traffic',
    help: 'Inspects tool arguments and responses for credentials (API keys, tokens, private keys, …) and flags them in the Activity log.',
    control: 'toggle',
  },
  {
    key: 'reveal_secret_headers',
    label: 'Show secret headers (debug)',
    help: 'Normally mcpproxy redacts Authorization / API-Key header values in responses and logs. Turning this on shows them in clear text — debugging only.',
    control: 'toggle',
    danger: {
      confirmValue: true,
      message:
        'Revealing secret headers will expose credentials in tool responses, logs and the Activity view. Only enable temporarily for debugging. Continue?',
    },
  },
  {
    key: 'listen',
    label: 'Listen address',
    help: 'Where the server accepts connections. Keep 127.0.0.1:8080 for local-only use. To reach mcpproxy from other machines use 0.0.0.0:8080 — turn on “Require API key for MCP clients” first. Takes effect after restart.',
    control: 'text',
    valueKind: 'hostport',
    restart: true,
    placeholder: '127.0.0.1:8080',
    danger: {
      message:
        'Binding to a non-loopback address (e.g. 0.0.0.0) exposes mcpproxy to your network. Make sure “Require API key for MCP clients” is enabled. Continue?',
    },
  },
]

// ---- Section 2: General ----
export const GENERAL_FIELDS: SettingField[] = [
  {
    key: 'routing_mode',
    docs: '/features/routing-modes',
    label: 'How agents find tools',
    help: 'Retrieve = agents search for tools first (best when you have many servers); Direct = every tool is listed to the agent; Code execution = agents call tools from JavaScript.',
    control: 'select',
    options: [
      { value: 'retrieve_tools', label: 'Retrieve — search tools first (recommended)' },
      { value: 'direct', label: 'Direct — list all tools' },
      { value: 'code_execution', label: 'Code execution' },
    ],
  },
  { key: 'tools_limit', label: 'Search results limit', help: 'How many tools a single tool-search returns to the agent.', control: 'number', min: 1, max: 1000 },
  { key: 'tool_response_limit', label: 'Max tool response size (characters)', help: 'Responses larger than this are truncated and cached so the agent can page through them. 0 = never truncate.', control: 'number', min: 0 },
  { key: 'call_tool_timeout', label: 'Tool call timeout', help: 'How long to wait for a single tool call before giving up. e.g. 2m, 90s, 30s.', control: 'duration', placeholder: '2m' },
  {
    key: 'logging.level',
    label: 'Log verbosity',
    help: 'How much detail mcpproxy writes to its logs. Use debug/trace when troubleshooting.',
    control: 'select',
    options: ['trace', 'debug', 'info', 'warn', 'error'].map((v) => ({ value: v, label: v })),
  },
  {
    key: 'telemetry.enabled',
    docs: '/features/telemetry',
    label: 'Anonymous usage telemetry',
    help: 'Sends anonymous usage counts (never tool arguments, content, or identities). Opt-out at any time.',
    control: 'toggle',
    danger: {
      confirmValue: false,
      tone: 'info',
      message:
        'Anonymous telemetry is how we see which features matter and catch problems — it never includes your tool arguments, content, or any identifying info. Turning it off removes that signal. Turn it off anyway?',
    },
  },
  { key: 'enable_prompts', label: 'Expose MCP prompts to clients', help: 'Advertises mcpproxy’s built-in guided prompts to connected AI clients: “setup-new-mcp-server” (add a server) and “troubleshoot-mcp-server” (diagnose connection issues).', control: 'toggle' },
]

// ---- Server edition (multi-user) section ----
// User-facing wording is "Server Edition" (MCP-1087). The backend rename of the
// top-level config key (`teams` -> `server_edition`, MCP-1086) has landed, so
// these dot-paths write/read the canonical `server_edition.*` key. Legacy
// `teams`-keyed configs still populate the form: the backend loader normalizes
// `teams` -> `server_edition` on load, and Settings.vue aliases it defensively
// so old configs hydrate the form while edits always save under `server_edition`.
export const SERVER_EDITION_TAB_LABEL = 'Server Edition'
export const SERVER_EDITION_SECTION_TITLE = '👥 Server Edition'
export const SERVER_EDITION_FIELDS: SettingField[] = [
  { key: 'server_edition.enabled', label: 'Enable multi-user mode', control: 'toggle', restart: true },
  { key: 'server_edition.oauth.provider', label: 'OAuth provider', control: 'select', options: ['', 'google', 'github', 'microsoft'].map((v) => ({ value: v, label: v || '(none)' })) },
  { key: 'server_edition.max_user_servers', label: 'Max servers per user', control: 'number', min: 0 },
]

// isBlankInstructions returns true when a saved instructions value is empty /
// whitespace-only / null / undefined — i.e. eligible for prefill from the built-in default.
export function isBlankInstructions(v: string | null | undefined): boolean {
  return !v || v.trim() === ''
}

// ---- Section 3: Advanced (subsystem accordions) ----
export const ADVANCED_ACCORDIONS: SettingsAccordion[] = [
  {
    id: 'mcp',
    title: 'MCP server instructions',
    description:
      'Text sent to AI clients in the MCP initialize response, guiding how to use the proxy. Power-user, set-once option.',
    fields: [
      {
        key: 'instructions',
        label: 'Server instructions',
        // Empty saves "" — Go maps that back to the built-in default, which is
        // shown here as the placeholder (fetched live so it never drifts from
        // the backend). Applied at startup / on the next client connect; it
        // does not hot-reload into already-connected MCP sessions.
        help: 'Leave blank to use the built-in default (shown greyed-out below). Applied on the next client connect, not to already-connected sessions.',
        control: 'textarea',
        optional: true,
        placeholder: 'Loading built-in default…',
      },
    ],
  },
  {
    id: 'code-execution',
    docs: '/features/code-execution',
    title: 'Code execution',
    description: 'Limits for the sandboxed JavaScript tool (enable it in Security & Access).',
    fields: [
      { key: 'code_execution_timeout_ms', label: 'Max run time per execution (ms)', control: 'number', min: 1, max: 600000 },
      { key: 'code_execution_max_tool_calls', label: 'Max tool calls per execution', help: '0 = unlimited.', control: 'number', min: 0 },
      { key: 'code_execution_pool_size', label: 'JavaScript runtime pool size', help: 'How many sandboxes run concurrently.', control: 'number', min: 1, max: 100 },
    ],
  },
  {
    id: 'docker-isolation',
    docs: '/features/docker-isolation',
    title: 'Docker isolation',
    description: 'Defaults for containerised stdio servers (turn isolation on in Security & Access). The per-runtime image map and extra docker args are edited in the Raw JSON tab.',
    fields: [
      { key: 'docker_isolation.network_mode', label: 'Container network', help: 'none = no network (most secure), bridge = NAT, host = share host network.', control: 'select', options: ['bridge', 'none', 'host'].map((v) => ({ value: v, label: v })) },
      { key: 'docker_isolation.memory_limit', label: 'Memory limit per container', control: 'text', placeholder: '512m', valueKind: 'bytesize', optional: true },
      { key: 'docker_isolation.cpu_limit', label: 'CPU limit per container', control: 'text', placeholder: '1.0', valueKind: 'cpu', optional: true },
      { key: 'docker_isolation.registry', label: 'Container image registry', control: 'text', placeholder: 'docker.io', valueKind: 'hostname', optional: true },
      { key: 'docker_isolation.enable_cache_volume', label: 'Share a package cache volume', help: 'Speeds up repeated npm/uvx installs by caching across containers.', control: 'toggle' },
    ],
  },
  {
    id: 'sensitive-data',
    docs: '/features/sensitive-data-detection',
    title: 'Secret detection',
    description: 'Tuning for the secret scanner (turn it on in Security & Access). Per-category toggles and custom patterns are edited in the Raw JSON tab.',
    fields: [
      { key: 'sensitive_data_detection.scan_requests', label: 'Scan tool arguments', control: 'toggle' },
      { key: 'sensitive_data_detection.scan_responses', label: 'Scan tool responses', control: 'toggle' },
      { key: 'sensitive_data_detection.max_payload_size_kb', label: 'Max payload scanned (KB)', help: 'Larger payloads are scanned only up to this size.', control: 'number', min: 1 },
      { key: 'sensitive_data_detection.entropy_threshold', label: 'Randomness threshold', help: 'Higher = fewer false positives when flagging random-looking strings (default 4.5).', control: 'number', min: 0, max: 8, step: 0.1 },
    ],
  },
  {
    id: 'output-validation',
    docs: '/features/output-schema-validation',
    title: 'Output-schema validation',
    description: 'Checks a tool’s structured response against its declared schema before it reaches the agent (Spec 054 Track A).',
    fields: [
      { key: 'output_validation.mode', label: 'Validation mode', help: 'off = no checks; warn = log mismatches; strict = block non-conforming responses.', control: 'select', options: ['off', 'warn', 'strict'].map((v) => ({ value: v, label: v })) },
      { key: 'output_validation.missing_structured_content', label: 'When structured content is missing', help: 'Whether to allow or block a response that declares a schema but returns none.', control: 'select', options: ['allow', 'block'].map((v) => ({ value: v, label: v })) },
    ],
  },
  {
    id: 'output-sanitisation',
    docs: '/features/output-sanitisation',
    title: 'Output sanitisation',
    description: 'Contains untrusted tool output before it reaches the agent (Spec 054 Track B). All opt-in.',
    fields: [
      { key: 'output_sanitisation.spotlight_untrusted', label: 'Wrap untrusted output in markers', help: 'Surrounds output from open-world tools with «untrusted» delimiters so the agent can tell data from instructions.', control: 'toggle' },
      { key: 'output_sanitisation.response_action', label: 'On detected secrets', help: 'spotlight = only wrap; redact = mask secrets; block = drop the whole response on a critical detection.', control: 'select', options: ['spotlight', 'redact', 'block'].map((v) => ({ value: v, label: v })) },
      { key: 'output_sanitisation.strip_control_chars', label: 'Strip control characters', help: 'Remove ANSI/zero-width/bidi sequences that can hide instructions in untrusted text.', control: 'toggle' },
      { key: 'output_sanitisation.strip_classes', label: 'Which characters to strip', control: 'multiselect', options: ['ansi', 'c0c1', 'bidi', 'zero_width'].map((v) => ({ value: v, label: v })) },
      { key: 'output_sanitisation.max_redactions', label: 'Max redactions per response', control: 'number', min: 0 },
    ],
  },
  {
    id: 'activity',
    docs: '/features/activity-log',
    title: 'Activity log retention',
    description: 'How long the audit log of tool calls is kept.',
    fields: [
      { key: 'activity_retention_days', label: 'Keep records for (days)', help: '0 = keep until the record cap is hit.', control: 'number', min: 0 },
      { key: 'activity_max_records', label: 'Maximum records kept', control: 'number', min: 0 },
      { key: 'activity_cleanup_interval_min', label: 'Cleanup runs every (minutes)', control: 'number', min: 1 },
    ],
  },
  {
    id: 'discovery',
    title: 'Tool discovery & health checks',
    description: 'How often mcpproxy probes upstream servers for liveness and re-discovers their tools. Lower these to reduce background traffic to chatty servers.',
    fields: [
      { key: 'health_check_interval', label: 'Health-check interval', help: 'How often to send a lightweight liveness ping to each connected server. "0s" disables the periodic probe (a dead server is then detected lazily on the next tool call). Range: 5s–1h. Default 30s. Does not apply to Docker-isolated servers — their liveness is monitored at the container level.', control: 'duration', placeholder: '30s', optional: true },
      { key: 'tool_discovery_interval', label: 'Tool-discovery interval', help: 'How often to re-list every server’s tools to rebuild the search index. "0s" disables the periodic sweep — tool changes are then picked up only at connect time and via tools/list_changed push notifications. Range: 30s–24h. Default 5m.', control: 'duration', placeholder: '5m', optional: true },
    ],
  },
  {
    id: 'logging',
    title: 'Logging',
    fields: [
      { key: 'logging.enable_file', label: 'Write logs to a file', control: 'toggle' },
      { key: 'logging.enable_console', label: 'Write logs to the console', control: 'toggle' },
      { key: 'logging.json_format', label: 'Structured (JSON) log format', control: 'toggle' },
      { key: 'logging.max_size', label: 'Rotate log after (MB)', control: 'number', min: 1 },
      { key: 'logging.max_backups', label: 'Rotated files to keep', control: 'number', min: 0 },
      { key: 'logging.max_age', label: 'Delete rotated logs after (days)', control: 'number', min: 0 },
    ],
  },
  {
    id: 'tls',
    title: 'TLS / HTTPS',
    description: 'Serve the API and Web UI over HTTPS. Changes take effect after a restart.',
    fields: [
      { key: 'tls.enabled', label: 'Serve over HTTPS', control: 'toggle', restart: true },
      { key: 'tls.require_client_cert', label: 'Require client certificate (mTLS)', control: 'toggle', restart: true },
      { key: 'tls.hsts', label: 'Send HSTS header', help: 'Tells browsers to always use HTTPS for this host.', control: 'toggle', restart: true },
    ],
  },
  {
    id: 'misc',
    title: 'Other',
    fields: [
      { key: 'max_result_size_chars', label: 'Max inline response to the client (characters)', help: 'Hard ceiling on a single response sent inline. 0 = no ceiling.', control: 'number', min: 0 },
      { key: 'oauth_expiry_warning_hours', label: 'Warn before OAuth token expires (hours)', help: 'How early a server is shown as “degraded” before its OAuth token expires.', control: 'number', min: 0, step: 0.5 },
      { key: 'disable_management', label: 'Block agents from managing servers', help: 'Prevents agents from using the upstream_servers management tool.', control: 'toggle', danger: { confirmValue: true, message: 'This prevents agents from adding or removing servers via the management tool. Continue?' } },
      { key: 'allow_server_add', label: 'Let agents add servers', control: 'toggle' },
      { key: 'allow_server_remove', label: 'Let agents remove servers', control: 'toggle' },
      { key: 'debug_search', label: 'Verbose tool-search debugging', control: 'toggle' },
    ],
  },
]

// ---- path helpers ----
// setPath guards every key against prototype-polluting names (__proto__,
// prototype, constructor) so a crafted dot-path can never reach Object.prototype.
// Settings keys come from the static catalogue above, but the generic helper is
// guarded regardless.

export function getPath(obj: any, path: string): any {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj)
}

export function setPath(obj: any, path: string, value: any): void {
  const keys = path.split('.')
  let cur = obj
  for (let i = 0; i < keys.length - 1; i++) {
    const k = keys[i]
    // Guard every traversal key against prototype pollution. The explicit
    // per-key comparison (rather than a Set lookup) is what CodeQL recognises
    // as a sanitising barrier for js/prototype-pollution-utility.
    if (k === '__proto__' || k === 'prototype' || k === 'constructor') return
    if (cur[k] == null || typeof cur[k] !== 'object') cur[k] = {}
    cur = cur[k]
  }
  const last = keys[keys.length - 1]
  if (last === '__proto__' || last === 'prototype' || last === 'constructor') return
  cur[last] = value
}

// buildPartial assembles a nested object containing ONLY the given dot-path
// keys, read from `source`. This is the partial payload sent to PATCH /config.
export function buildPartial(source: any, dirtyKeys: string[]): Record<string, any> {
  const out: Record<string, any> = {}
  for (const key of dirtyKeys) {
    setPath(out, key, getPath(source, key))
  }
  return out
}
