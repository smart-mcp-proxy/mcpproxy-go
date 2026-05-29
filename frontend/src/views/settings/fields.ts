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
}

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
}

export interface SettingsAccordion {
  id: string
  title: string
  description?: string
  fields: SettingField[]
}

// ---- Section 1: Security & Access (prioritised, security-first) ----
export const SECURITY_FIELDS: SettingField[] = [
  {
    key: 'api_key',
    label: 'API key',
    help: 'Secret that authenticates clients to the REST API and Web UI (sent as the "X-API-Key" header or "?apikey=" in the URL). Leave blank to keep the current key; ↻ generates a new one. Changing it requires a restart and re-connecting clients.',
    control: 'secret',
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
    label: 'Run stdio servers in Docker',
    help: 'Runs command-based (stdio) MCP servers inside isolated Docker containers instead of directly on your machine. Requires Docker to be installed and running.',
    control: 'toggle',
  },
  {
    key: 'enable_code_execution',
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
  { key: 'telemetry.enabled', label: 'Anonymous usage telemetry', help: 'Sends anonymous usage counts (never tool arguments, content, or identities). Opt-out at any time.', control: 'toggle' },
  { key: 'enable_prompts', label: 'Expose MCP prompts to clients', help: 'Advertise prompt templates from your upstream servers to connected AI clients.', control: 'toggle' },
]

// ---- Section 3: Advanced (subsystem accordions) ----
export const ADVANCED_ACCORDIONS: SettingsAccordion[] = [
  {
    id: 'code-execution',
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
    title: 'Docker isolation',
    description: 'Defaults for containerised stdio servers (turn isolation on in Security & Access). The per-runtime image map and extra docker args are edited in the Raw JSON tab.',
    fields: [
      { key: 'docker_isolation.network_mode', label: 'Container network', help: 'none = no network (most secure), bridge = NAT, host = share host network.', control: 'select', options: ['bridge', 'none', 'host'].map((v) => ({ value: v, label: v })) },
      { key: 'docker_isolation.memory_limit', label: 'Memory limit per container', control: 'text', placeholder: '512m' },
      { key: 'docker_isolation.cpu_limit', label: 'CPU limit per container', control: 'text', placeholder: '1.0' },
      { key: 'docker_isolation.registry', label: 'Container image registry', control: 'text', placeholder: 'docker.io' },
      { key: 'docker_isolation.enable_cache_volume', label: 'Share a package cache volume', help: 'Speeds up repeated npm/uvx installs by caching across containers.', control: 'toggle' },
    ],
  },
  {
    id: 'sensitive-data',
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
    title: 'Output-schema validation',
    description: 'Checks a tool’s structured response against its declared schema before it reaches the agent (Spec 054 Track A).',
    fields: [
      { key: 'output_validation.mode', label: 'Validation mode', help: 'off = no checks; warn = log mismatches; strict = block non-conforming responses.', control: 'select', options: ['off', 'warn', 'strict'].map((v) => ({ value: v, label: v })) },
      { key: 'output_validation.missing_structured_content', label: 'When structured content is missing', help: 'Whether to allow or block a response that declares a schema but returns none.', control: 'select', options: ['allow', 'block'].map((v) => ({ value: v, label: v })) },
    ],
  },
  {
    id: 'output-sanitisation',
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
    title: 'Activity log retention',
    description: 'How long the audit log of tool calls is kept.',
    fields: [
      { key: 'activity_retention_days', label: 'Keep records for (days)', help: '0 = keep until the record cap is hit.', control: 'number', min: 0 },
      { key: 'activity_max_records', label: 'Maximum records kept', control: 'number', min: 0 },
      { key: 'activity_cleanup_interval_min', label: 'Cleanup runs every (minutes)', control: 'number', min: 1 },
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
// Keys that would mutate the prototype chain. setPath refuses to traverse or
// assign these so a crafted dot-path can never pollute Object.prototype
// (CodeQL js/prototype-polluting-assignment). Settings keys come from the
// static catalogue above, but we guard the generic helper regardless.
const UNSAFE_KEYS = new Set(['__proto__', 'prototype', 'constructor'])

export function getPath(obj: any, path: string): any {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj)
}

export function setPath(obj: any, path: string, value: any): void {
  const keys = path.split('.')
  if (keys.some((k) => UNSAFE_KEYS.has(k))) return // guard against prototype pollution
  let cur = obj
  for (let i = 0; i < keys.length - 1; i++) {
    if (cur[keys[i]] == null || typeof cur[keys[i]] !== 'object') cur[keys[i]] = {}
    cur = cur[keys[i]]
  }
  cur[keys[keys.length - 1]] = value
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
