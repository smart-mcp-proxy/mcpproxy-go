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
    help: 'Secret for REST API authentication. Leave blank to keep the current key.',
    control: 'secret',
    restart: true,
    placeholder: '•••••••• (unchanged)',
  },
  {
    key: 'require_mcp_auth',
    label: 'Require auth on /mcp',
    help: 'Reject unauthenticated requests to the MCP endpoint.',
    control: 'toggle',
  },
  {
    key: 'quarantine_enabled',
    label: 'Security quarantine',
    help: 'Auto-quarantine new servers and require approval for changed tools (Tool Poisoning protection).',
    control: 'toggle',
    danger: {
      confirmValue: false,
      message:
        'Disabling quarantine removes Tool Poisoning Attack protection — new servers and changed tools will run without approval. Continue?',
    },
  },
  {
    key: 'docker_isolation.enabled',
    label: 'Global Docker isolation',
    help: 'Run stdio MCP servers inside Docker containers by default.',
    control: 'toggle',
  },
  {
    key: 'enable_code_execution',
    label: 'Code execution tool',
    help: 'Expose the sandboxed JavaScript code_execution tool.',
    control: 'toggle',
  },
  {
    key: 'read_only_mode',
    label: 'Read-only mode',
    help: 'Block all write and destructive tool calls.',
    control: 'toggle',
  },
  {
    key: 'sensitive_data_detection.enabled',
    label: 'Sensitive-data detection',
    help: 'Scan tool calls and responses for secrets and credentials.',
    control: 'toggle',
  },
  {
    key: 'reveal_secret_headers',
    label: 'Reveal secret headers',
    help: 'Stop redacting Authorization / API-Key headers in responses and logs.',
    control: 'toggle',
    danger: {
      confirmValue: true,
      message:
        'Revealing secret headers will expose credentials in tool responses, logs and the activity view. Only enable for debugging. Continue?',
    },
  },
  {
    key: 'listen',
    label: 'Listen address',
    help: 'Network bind address. Keep 127.0.0.1 unless you intend remote access.',
    control: 'text',
    restart: true,
    placeholder: '127.0.0.1:8080',
    danger: {
      message:
        'Binding to a non-loopback address exposes mcpproxy on your network. Make sure authentication is enabled. Continue?',
    },
  },
]

// ---- Section 2: General ----
export const GENERAL_FIELDS: SettingField[] = [
  {
    key: 'routing_mode',
    label: 'Routing mode',
    help: 'How upstream tools are exposed to agents.',
    control: 'select',
    options: [
      { value: 'retrieve_tools', label: 'Retrieve tools (BM25 discovery)' },
      { value: 'direct', label: 'Direct (all tools listed)' },
      { value: 'code_execution', label: 'Code execution' },
    ],
  },
  { key: 'tools_limit', label: 'Tools per search', help: 'Max tools returned by retrieve_tools.', control: 'number', min: 1, max: 1000 },
  { key: 'tool_response_limit', label: 'Tool response limit (chars)', help: '0 disables truncation.', control: 'number', min: 0 },
  { key: 'call_tool_timeout', label: 'Tool call timeout', help: 'e.g. 2m, 90s.', control: 'duration', placeholder: '2m' },
  {
    key: 'logging.level',
    label: 'Log level',
    control: 'select',
    options: ['trace', 'debug', 'info', 'warn', 'error'].map((v) => ({ value: v, label: v })),
  },
  { key: 'telemetry.enabled', label: 'Anonymous telemetry', help: 'Opt-out anonymous usage metrics.', control: 'toggle' },
  { key: 'enable_prompts', label: 'Expose MCP prompts', control: 'toggle' },
]

// ---- Section 3: Advanced (subsystem accordions) ----
export const ADVANCED_ACCORDIONS: SettingsAccordion[] = [
  {
    id: 'code-execution',
    title: 'Code execution',
    fields: [
      { key: 'code_execution_timeout_ms', label: 'Timeout (ms)', control: 'number', min: 1, max: 600000 },
      { key: 'code_execution_max_tool_calls', label: 'Max tool calls (0 = unlimited)', control: 'number', min: 0 },
      { key: 'code_execution_pool_size', label: 'Runtime pool size', control: 'number', min: 1, max: 100 },
    ],
  },
  {
    id: 'docker-isolation',
    title: 'Docker isolation',
    description: 'Per-image and resource detail. Image map / extra args are editable in the Raw JSON tab.',
    fields: [
      { key: 'docker_isolation.network_mode', label: 'Network mode', control: 'select', options: ['bridge', 'none', 'host'].map((v) => ({ value: v, label: v })) },
      { key: 'docker_isolation.memory_limit', label: 'Memory limit', control: 'text', placeholder: '512m' },
      { key: 'docker_isolation.cpu_limit', label: 'CPU limit', control: 'text', placeholder: '1.0' },
      { key: 'docker_isolation.registry', label: 'Registry', control: 'text', placeholder: 'docker.io' },
      { key: 'docker_isolation.enable_cache_volume', label: 'Cache volume', control: 'toggle' },
    ],
  },
  {
    id: 'sensitive-data',
    title: 'Sensitive-data detection',
    description: 'Per-category toggles and custom patterns are editable in the Raw JSON tab.',
    fields: [
      { key: 'sensitive_data_detection.scan_requests', label: 'Scan tool arguments', control: 'toggle' },
      { key: 'sensitive_data_detection.scan_responses', label: 'Scan tool responses', control: 'toggle' },
      { key: 'sensitive_data_detection.max_payload_size_kb', label: 'Max scan size (KB)', control: 'number', min: 1 },
      { key: 'sensitive_data_detection.entropy_threshold', label: 'Entropy threshold', control: 'number', min: 0, max: 8, step: 0.1 },
    ],
  },
  {
    id: 'output-validation',
    title: 'Output-schema validation (Track A)',
    fields: [
      { key: 'output_validation.mode', label: 'Mode', control: 'select', options: ['off', 'warn', 'strict'].map((v) => ({ value: v, label: v })) },
      { key: 'output_validation.missing_structured_content', label: 'Missing structured content', control: 'select', options: ['allow', 'block'].map((v) => ({ value: v, label: v })) },
    ],
  },
  {
    id: 'output-sanitisation',
    title: 'Output sanitisation (Track B)',
    fields: [
      { key: 'output_sanitisation.spotlight_untrusted', label: 'Spotlight untrusted output', control: 'toggle' },
      { key: 'output_sanitisation.response_action', label: 'Response action', control: 'select', options: ['spotlight', 'redact', 'block'].map((v) => ({ value: v, label: v })) },
      { key: 'output_sanitisation.strip_control_chars', label: 'Strip control sequences', control: 'toggle' },
      { key: 'output_sanitisation.strip_classes', label: 'Strip classes', control: 'multiselect', options: ['ansi', 'c0c1', 'bidi', 'zero_width'].map((v) => ({ value: v, label: v })) },
      { key: 'output_sanitisation.max_redactions', label: 'Max redactions / response', control: 'number', min: 0 },
    ],
  },
  {
    id: 'activity',
    title: 'Activity log retention',
    fields: [
      { key: 'activity_retention_days', label: 'Retention (days)', control: 'number', min: 0 },
      { key: 'activity_max_records', label: 'Max records', control: 'number', min: 0 },
      { key: 'activity_cleanup_interval_min', label: 'Cleanup interval (min)', control: 'number', min: 1 },
    ],
  },
  {
    id: 'logging',
    title: 'Logging',
    fields: [
      { key: 'logging.enable_file', label: 'Log to file', control: 'toggle' },
      { key: 'logging.enable_console', label: 'Log to console', control: 'toggle' },
      { key: 'logging.json_format', label: 'JSON format', control: 'toggle' },
      { key: 'logging.max_size', label: 'Max size (MB)', control: 'number', min: 1 },
      { key: 'logging.max_backups', label: 'Max backups', control: 'number', min: 0 },
      { key: 'logging.max_age', label: 'Max age (days)', control: 'number', min: 0 },
    ],
  },
  {
    id: 'tls',
    title: 'TLS / HTTPS',
    fields: [
      { key: 'tls.enabled', label: 'Enable HTTPS', control: 'toggle', restart: true },
      { key: 'tls.require_client_cert', label: 'Require client cert (mTLS)', control: 'toggle', restart: true },
      { key: 'tls.hsts', label: 'HSTS header', control: 'toggle', restart: true },
    ],
  },
  {
    id: 'misc',
    title: 'Miscellaneous',
    fields: [
      { key: 'max_result_size_chars', label: 'Max inline response (chars, 0 = off)', control: 'number', min: 0 },
      { key: 'oauth_expiry_warning_hours', label: 'OAuth expiry warning (hours)', control: 'number', min: 0, step: 0.5 },
      { key: 'disable_management', label: 'Disable management tool', control: 'toggle', danger: { confirmValue: true, message: 'Disabling the management tool prevents agents from adding/removing servers. Continue?' } },
      { key: 'allow_server_add', label: 'Allow agents to add servers', control: 'toggle' },
      { key: 'allow_server_remove', label: 'Allow agents to remove servers', control: 'toggle' },
      { key: 'debug_search', label: 'Debug BM25 search', control: 'toggle' },
    ],
  },
]

// ---- path helpers ----
export function getPath(obj: any, path: string): any {
  return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj)
}

export function setPath(obj: any, path: string, value: any): void {
  const keys = path.split('.')
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
