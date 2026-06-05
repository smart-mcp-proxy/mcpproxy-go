// Spec 061 — declarative settings field catalogue (Swift port of the Web UI's
// frontend/src/views/settings/fields.ts from Spec 060).
//
// Each entry maps a dot-path into the mcpproxy config to a labelled control.
// Keeping this declarative makes the tray Settings sheet auditable ("every
// non-deprecated option reachable") and mirrors the Web UI 1:1.

import Foundation

// MARK: - Pinned types (view layer depends on these — do not rename)

enum ConfigControl { case toggle, select, number, text, secret, duration, multiselect }
enum ConfigValueKind { case hostport, bytesize, cpu, hostname, url, secretkey }

struct ConfigOption: Identifiable { let value: String; let label: String; var id: String { value } }

struct ConfigField: Identifiable {
    let key: String                 // dot-path, e.g. "docker_isolation.enabled"
    let label: String
    var help: String? = nil
    let control: ConfigControl
    var options: [ConfigOption] = []
    var min: Double? = nil
    var max: Double? = nil
    var restart: Bool = false
    var docs: String? = nil         // doc path on docs.mcpproxy.app
    var valueKind: ConfigValueKind? = nil
    var optional: Bool = false
    // Danger confirm: present a confirm dialog before saving. For toggles,
    // only when the new value == dangerConfirmValue (nil = always confirm).
    var dangerMessage: String? = nil
    var dangerConfirmValue: Bool? = nil
    var dangerInfoTone: Bool = false  // gentle "are you sure" (telemetry), not red
    var id: String { key }
}

struct ConfigSection: Identifiable {
    let id: String
    let title: String
    var help: String? = nil
    var docs: String? = nil
    let fields: [ConfigField]
}

// MARK: - Catalogue

enum SettingsCatalog {
    static let docsBase = "https://docs.mcpproxy.app"

    /// Absolute docs URL for a field/section `docs` path, or nil.
    static func docsURL(_ path: String?) -> String? {
        guard let path = path, !path.isEmpty else { return nil }
        return path.hasPrefix("http") ? path : docsBase + path
    }

    // ---- Section 1: Security & Access (prioritised, security-first) ----
    static let security: [ConfigField] = [
        ConfigField(
            key: "api_key",
            label: "API key",
            help: "Secret that authenticates clients to the REST API and Web UI (sent as the \"X-API-Key\" header or \"?apikey=\" in the URL). Leave blank to keep the current key; ↻ generates a new one. Changing it requires a restart and re-connecting clients.",
            control: .secret,
            restart: true,
            valueKind: .secretkey,
            optional: true
        ),
        ConfigField(
            key: "require_mcp_auth",
            label: "Require API key for MCP clients",
            help: "When on, AI clients connecting to the /mcp endpoint must send the API key above (as an \"Authorization: Bearer <key>\" header). Use \u{201C}Connect a client\u{201D} to configure this automatically. When off, any local client can connect without a key.",
            control: .toggle
        ),
        ConfigField(
            key: "quarantine_enabled",
            label: "Quarantine new servers & changed tools",
            help: "Holds newly added servers and tools whose description/schema changed for your approval before agents can call them — protects against Tool Poisoning Attacks. Recommended ON.",
            control: .toggle,
            docs: "/features/security-quarantine",
            dangerMessage: "Disabling quarantine removes Tool Poisoning Attack protection — new servers and changed tools will run without your approval. Continue?",
            dangerConfirmValue: false
        ),
        ConfigField(
            key: "docker_isolation.enabled",
            label: "Run stdio servers in Docker",
            help: "Runs command-based (stdio) MCP servers inside isolated Docker containers instead of directly on your machine. Requires Docker to be installed and running.",
            control: .toggle,
            docs: "/features/docker-isolation"
        ),
        ConfigField(
            key: "enable_code_execution",
            label: "Enable code execution tool",
            help: "Adds a sandboxed JavaScript tool agents can use to orchestrate several tool calls in one request. Off by default.",
            control: .toggle,
            docs: "/features/code-execution"
        ),
        ConfigField(
            key: "read_only_mode",
            label: "Read-only mode",
            help: "Blocks every write and destructive tool call across all servers — agents can only read. Useful for safe exploration.",
            control: .toggle
        ),
        ConfigField(
            key: "sensitive_data_detection.enabled",
            label: "Scan for secrets in tool traffic",
            help: "Inspects tool arguments and responses for credentials (API keys, tokens, private keys, …) and flags them in the Activity log.",
            control: .toggle,
            docs: "/features/sensitive-data-detection"
        ),
        ConfigField(
            key: "reveal_secret_headers",
            label: "Show secret headers (debug)",
            help: "Normally mcpproxy redacts Authorization / API-Key header values in responses and logs. Turning this on shows them in clear text — debugging only.",
            control: .toggle,
            dangerMessage: "Revealing secret headers will expose credentials in tool responses, logs and the Activity view. Only enable temporarily for debugging. Continue?",
            dangerConfirmValue: true
        ),
        ConfigField(
            key: "listen",
            label: "Listen address",
            help: "Where the server accepts connections. Keep 127.0.0.1:8080 for local-only use. To reach mcpproxy from other machines use 0.0.0.0:8080 — turn on \u{201C}Require API key for MCP clients\u{201D} first. Takes effect after restart.",
            control: .text,
            restart: true,
            valueKind: .hostport,
            dangerMessage: "Binding to a non-loopback address (e.g. 0.0.0.0) exposes mcpproxy to your network. Make sure \u{201C}Require API key for MCP clients\u{201D} is enabled. Continue?"
        ),
    ]

    // ---- Section 2: General ----
    static let general: [ConfigField] = [
        ConfigField(
            key: "routing_mode",
            label: "How agents find tools",
            help: "Retrieve = agents search for tools first (best when you have many servers); Direct = every tool is listed to the agent; Code execution = agents call tools from JavaScript.",
            control: .select,
            options: [
                ConfigOption(value: "retrieve_tools", label: "Retrieve — search tools first (recommended)"),
                ConfigOption(value: "direct", label: "Direct — list all tools"),
                ConfigOption(value: "code_execution", label: "Code execution"),
            ],
            docs: "/features/routing-modes"
        ),
        ConfigField(
            key: "tools_limit",
            label: "Search results limit",
            help: "How many tools a single tool-search returns to the agent.",
            control: .number,
            min: 1,
            max: 1000
        ),
        ConfigField(
            key: "tool_response_limit",
            label: "Max tool response size (characters)",
            help: "Responses larger than this are truncated and cached so the agent can page through them. 0 = never truncate.",
            control: .number,
            min: 0
        ),
        ConfigField(
            key: "call_tool_timeout",
            label: "Tool call timeout",
            help: "How long to wait for a single tool call before giving up. e.g. 2m, 90s, 30s.",
            control: .duration
        ),
        ConfigField(
            key: "logging.level",
            label: "Log verbosity",
            help: "How much detail mcpproxy writes to its logs. Use debug/trace when troubleshooting.",
            control: .select,
            options: ["trace", "debug", "info", "warn", "error"].map { ConfigOption(value: $0, label: $0) }
        ),
        ConfigField(
            key: "telemetry.enabled",
            label: "Anonymous usage telemetry",
            help: "Sends anonymous usage counts (never tool arguments, content, or identities). Opt-out at any time.",
            control: .toggle,
            docs: "/features/telemetry",
            dangerMessage: "Anonymous telemetry is how we see which features matter and catch problems — it never includes your tool arguments, content, or any identifying info. Turning it off removes that signal. Turn it off anyway?",
            dangerConfirmValue: false,
            dangerInfoTone: true
        ),
        ConfigField(
            key: "enable_prompts",
            label: "Expose MCP prompts to clients",
            help: "Advertises mcpproxy\u{2019}s built-in guided prompts to connected AI clients: \u{201C}setup-new-mcp-server\u{201D} (add a server) and \u{201C}troubleshoot-mcp-server\u{201D} (diagnose connection issues).",
            control: .toggle
        ),
    ]

    // ---- Section 3: Advanced (subsystem accordions) ----
    static let advanced: [ConfigSection] = [
        ConfigSection(
            id: "code-execution",
            title: "Code execution",
            help: "Limits for the sandboxed JavaScript tool (enable it in Security & Access).",
            docs: "/features/code-execution",
            fields: [
                ConfigField(key: "code_execution_timeout_ms", label: "Max run time per execution (ms)", control: .number, min: 1, max: 600000),
                ConfigField(key: "code_execution_max_tool_calls", label: "Max tool calls per execution", help: "0 = unlimited.", control: .number, min: 0),
                ConfigField(key: "code_execution_pool_size", label: "JavaScript runtime pool size", help: "How many sandboxes run concurrently.", control: .number, min: 1, max: 100),
            ]
        ),
        ConfigSection(
            id: "docker-isolation",
            title: "Docker isolation",
            help: "Defaults for containerised stdio servers (turn isolation on in Security & Access). The per-runtime image map and extra docker args are edited in the Raw JSON tab.",
            docs: "/features/docker-isolation",
            fields: [
                ConfigField(key: "docker_isolation.network_mode", label: "Container network", help: "none = no network (most secure), bridge = NAT, host = share host network.", control: .select, options: ["bridge", "none", "host"].map { ConfigOption(value: $0, label: $0) }),
                ConfigField(key: "docker_isolation.memory_limit", label: "Memory limit per container", control: .text, valueKind: .bytesize, optional: true),
                ConfigField(key: "docker_isolation.cpu_limit", label: "CPU limit per container", control: .text, valueKind: .cpu, optional: true),
                ConfigField(key: "docker_isolation.registry", label: "Container image registry", control: .text, valueKind: .hostname, optional: true),
                ConfigField(key: "docker_isolation.enable_cache_volume", label: "Share a package cache volume", help: "Speeds up repeated npm/uvx installs by caching across containers.", control: .toggle),
            ]
        ),
        ConfigSection(
            id: "sensitive-data",
            title: "Secret detection",
            help: "Tuning for the secret scanner (turn it on in Security & Access). Per-category toggles and custom patterns are edited in the Raw JSON tab.",
            docs: "/features/sensitive-data-detection",
            fields: [
                ConfigField(key: "sensitive_data_detection.scan_requests", label: "Scan tool arguments", control: .toggle),
                ConfigField(key: "sensitive_data_detection.scan_responses", label: "Scan tool responses", control: .toggle),
                ConfigField(key: "sensitive_data_detection.max_payload_size_kb", label: "Max payload scanned (KB)", help: "Larger payloads are scanned only up to this size.", control: .number, min: 1),
                ConfigField(key: "sensitive_data_detection.entropy_threshold", label: "Randomness threshold", help: "Higher = fewer false positives when flagging random-looking strings (default 4.5).", control: .number, min: 0, max: 8),
            ]
        ),
        ConfigSection(
            id: "output-validation",
            title: "Output-schema validation",
            help: "Checks a tool\u{2019}s structured response against its declared schema before it reaches the agent (Spec 054 Track A).",
            docs: "/features/output-schema-validation",
            fields: [
                ConfigField(key: "output_validation.mode", label: "Validation mode", help: "off = no checks; warn = log mismatches; strict = block non-conforming responses.", control: .select, options: ["off", "warn", "strict"].map { ConfigOption(value: $0, label: $0) }),
                ConfigField(key: "output_validation.missing_structured_content", label: "When structured content is missing", help: "Whether to allow or block a response that declares a schema but returns none.", control: .select, options: ["allow", "block"].map { ConfigOption(value: $0, label: $0) }),
            ]
        ),
        ConfigSection(
            id: "output-sanitisation",
            title: "Output sanitisation",
            help: "Contains untrusted tool output before it reaches the agent (Spec 054 Track B). All opt-in.",
            docs: "/features/output-sanitisation",
            fields: [
                ConfigField(key: "output_sanitisation.spotlight_untrusted", label: "Wrap untrusted output in markers", help: "Surrounds output from open-world tools with «untrusted» delimiters so the agent can tell data from instructions.", control: .toggle),
                ConfigField(key: "output_sanitisation.response_action", label: "On detected secrets", help: "spotlight = only wrap; redact = mask secrets; block = drop the whole response on a critical detection.", control: .select, options: ["spotlight", "redact", "block"].map { ConfigOption(value: $0, label: $0) }),
                ConfigField(key: "output_sanitisation.strip_control_chars", label: "Strip control characters", help: "Remove ANSI/zero-width/bidi sequences that can hide instructions in untrusted text.", control: .toggle),
                ConfigField(key: "output_sanitisation.strip_classes", label: "Which characters to strip", control: .multiselect, options: ["ansi", "c0c1", "bidi", "zero_width"].map { ConfigOption(value: $0, label: $0) }),
                ConfigField(key: "output_sanitisation.max_redactions", label: "Max redactions per response", control: .number, min: 0),
            ]
        ),
        ConfigSection(
            id: "activity",
            title: "Activity log retention",
            help: "How long the audit log of tool calls is kept.",
            docs: "/features/activity-log",
            fields: [
                ConfigField(key: "activity_retention_days", label: "Keep records for (days)", help: "0 = keep until the record cap is hit.", control: .number, min: 0),
                ConfigField(key: "activity_max_records", label: "Maximum records kept", control: .number, min: 0),
                ConfigField(key: "activity_cleanup_interval_min", label: "Cleanup runs every (minutes)", control: .number, min: 1),
            ]
        ),
        ConfigSection(
            id: "discovery",
            title: "Tool discovery & health checks",
            help: "How often mcpproxy probes upstream servers for liveness and re-discovers their tools. Lower these to reduce background traffic to chatty servers.",
            fields: [
                ConfigField(key: "health_check_interval", label: "Health-check interval", help: "How often to send a lightweight liveness ping to each connected server. \"0s\" disables the periodic probe (a dead server is then detected lazily on the next tool call). Range: 5s\u{2013}1h. Default 30s.", control: .duration, optional: true),
                ConfigField(key: "tool_discovery_interval", label: "Tool-discovery interval", help: "How often to re-list every server\u{2019}s tools to rebuild the search index. \"0s\" disables the periodic sweep \u{2014} tool changes are then picked up only at connect time and via tools/list_changed push notifications. Range: 30s\u{2013}24h. Default 5m.", control: .duration, optional: true),
            ]
        ),
        ConfigSection(
            id: "logging",
            title: "Logging",
            fields: [
                ConfigField(key: "logging.enable_file", label: "Write logs to a file", control: .toggle),
                ConfigField(key: "logging.enable_console", label: "Write logs to the console", control: .toggle),
                ConfigField(key: "logging.json_format", label: "Structured (JSON) log format", control: .toggle),
                ConfigField(key: "logging.max_size", label: "Rotate log after (MB)", control: .number, min: 1),
                ConfigField(key: "logging.max_backups", label: "Rotated files to keep", control: .number, min: 0),
                ConfigField(key: "logging.max_age", label: "Delete rotated logs after (days)", control: .number, min: 0),
            ]
        ),
        ConfigSection(
            id: "tls",
            title: "TLS / HTTPS",
            help: "Serve the API and Web UI over HTTPS. Changes take effect after a restart.",
            fields: [
                ConfigField(key: "tls.enabled", label: "Serve over HTTPS", control: .toggle, restart: true),
                ConfigField(key: "tls.require_client_cert", label: "Require client certificate (mTLS)", control: .toggle, restart: true),
                ConfigField(key: "tls.hsts", label: "Send HSTS header", help: "Tells browsers to always use HTTPS for this host.", control: .toggle, restart: true),
            ]
        ),
        ConfigSection(
            id: "misc",
            title: "Other",
            fields: [
                ConfigField(key: "max_result_size_chars", label: "Max inline response to the client (characters)", help: "Hard ceiling on a single response sent inline. 0 = no ceiling.", control: .number, min: 0),
                ConfigField(key: "oauth_expiry_warning_hours", label: "Warn before OAuth token expires (hours)", help: "How early a server is shown as \u{201C}degraded\u{201D} before its OAuth token expires.", control: .number, min: 0),
                ConfigField(key: "disable_management", label: "Block agents from managing servers", help: "Prevents agents from using the upstream_servers management tool.", control: .toggle, dangerMessage: "This prevents agents from adding or removing servers via the management tool. Continue?", dangerConfirmValue: true),
                ConfigField(key: "allow_server_add", label: "Let agents add servers", control: .toggle),
                ConfigField(key: "allow_server_remove", label: "Let agents remove servers", control: .toggle),
                ConfigField(key: "debug_search", label: "Verbose tool-search debugging", control: .toggle),
            ]
        ),
    ]
}

// MARK: - Path helpers (operate on a JSON-decoded config dictionary)

/// Dot-path lookup. Returns the nested value or nil if any segment is missing.
func configGet(_ obj: [String: Any], _ path: String) -> Any? {
    let keys = path.split(separator: ".").map(String.init)
    guard !keys.isEmpty else { return nil }
    var current: Any = obj
    for key in keys {
        guard let dict = current as? [String: Any], let next = dict[key] else { return nil }
        current = next
    }
    return current
}

/// Dot-path set, creating intermediate dictionaries as needed. Empty path
/// components are skipped defensively. A nil value writes NSNull (explicit null)
/// — callers that want to drop a key should remove it instead.
func configSet(_ obj: inout [String: Any], _ path: String, _ value: Any?) {
    let keys = path.split(separator: ".").map(String.init).filter { !$0.isEmpty }
    guard !keys.isEmpty else { return }

    func assign(_ dict: inout [String: Any], _ keys: ArraySlice<String>) {
        let key = keys[keys.startIndex]
        if keys.count == 1 {
            dict[key] = value ?? NSNull()
            return
        }
        var child = (dict[key] as? [String: Any]) ?? [:]
        assign(&child, keys.dropFirst())
        dict[key] = child
    }
    assign(&obj, keys[...])
}

/// Assembles a nested object containing ONLY the given dot-path keys, read from
/// `source`. This is the partial payload sent to PATCH /config.
func buildPartial(_ source: [String: Any], _ keys: [String]) -> [String: Any] {
    var out: [String: Any] = [:]
    for key in keys {
        if let value = configGet(source, key) {
            configSet(&out, key, value)
        }
    }
    return out
}

// MARK: - Validation

private let durationRegex = try! NSRegularExpression(
    pattern: "^(\\d+(\\.\\d+)?(ns|us|µs|ms|s|m|h))+$"
)
private let portDigitsRegex = try! NSRegularExpression(pattern: "^\\d+$")
private let hostCharsRegex = try! NSRegularExpression(pattern: "^[A-Za-z0-9.\\-:]+$")
private let byteSizeRegex = try! NSRegularExpression(pattern: "^\\d+(\\.\\d+)?\\s*([bkmgtBKMGT]i?b?)?$")
private let cpuRegex = try! NSRegularExpression(pattern: "^\\d+(\\.\\d+)?$")
private let hostnameRegex = try! NSRegularExpression(pattern: "^[A-Za-z0-9.\\-]+(:\\d+)?(/[^\\s]*)?$")
private let urlRegex = try! NSRegularExpression(pattern: "^https?://[^\\s]+$")

private func matches(_ regex: NSRegularExpression, _ s: String) -> Bool {
    let range = NSRange(s.startIndex..<s.endIndex, in: s)
    return regex.firstMatch(in: s, range: range) != nil
}

/// validateHostPort checks a Go-style listen address: "[host]:port" where the
/// host part may be empty (all interfaces), an IPv4/hostname, or a bracketed
/// IPv6 literal. Port must be 1–65535.
private func validateHostPort(_ s: String) -> String? {
    let host: String
    let portStr: String
    if s.hasPrefix("[") {
        guard let closeIdx = s.firstIndex(of: "]") else { return "Unclosed \u{201C}[\u{201D} in IPv6 address" }
        host = String(s[s.index(after: s.startIndex)..<closeIdx])
        let rest = String(s[s.index(after: closeIdx)...])
        guard rest.hasPrefix(":") else { return "Expected [ipv6]:port, e.g. [::1]:8080" }
        portStr = String(rest.dropFirst())
    } else {
        guard let i = s.lastIndex(of: ":") else { return "Must include a port, e.g. 127.0.0.1:8080" }
        host = String(s[s.startIndex..<i])
        portStr = String(s[s.index(after: i)...])
    }
    guard matches(portDigitsRegex, portStr) else { return "Port must be a number, e.g. 8080" }
    guard let port = Int(portStr) else { return "Port must be a number, e.g. 8080" }
    if port < 1 || port > 65535 { return "Port must be between 1 and 65535" }
    if !host.isEmpty && !matches(hostCharsRegex, host) { return "Invalid host in address" }
    return nil
}

/// Coerce a JSON-decoded value into a Double if numeric (Int, Double, NSNumber),
/// or by parsing a String. Returns nil when not numeric / empty.
private func numericValue(_ value: Any?) -> Double? {
    guard let value = value else { return nil }
    if let n = value as? NSNumber { return n.doubleValue }
    if let d = value as? Double { return d }
    if let i = value as? Int { return Double(i) }
    if let s = value as? String {
        let trimmed = s.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return nil }
        return Double(trimmed)
    }
    return nil
}

/// Render a JSON-decoded value as the trimmed string the validators expect.
private func stringValue(_ value: Any?) -> String {
    guard let value = value else { return "" }
    if let s = value as? String { return s.trimmingCharacters(in: .whitespaces) }
    if value is NSNull { return "" }
    if let n = value as? NSNumber { return n.stringValue.trimmingCharacters(in: .whitespaces) }
    return String(describing: value).trimmingCharacters(in: .whitespaces)
}

/// validateConfigField returns a human-readable error string for an invalid
/// value, or nil when the value is acceptable. Mirrors validateField in fields.ts.
func validateConfigField(_ field: ConfigField, _ value: Any?) -> String? {
    if field.control == .number {
        // Empty / null → "Enter a number"
        if value == nil || value is NSNull { return "Enter a number" }
        if let s = value as? String, s.trimmingCharacters(in: .whitespaces).isEmpty {
            return "Enter a number"
        }
        guard let n = numericValue(value) else { return "Must be a number" }
        if n.isNaN { return "Must be a number" }
        if let lo = field.min, n < lo { return "Must be ≥ \(formatBound(lo))" }
        if let hi = field.max, n > hi { return "Must be ≤ \(formatBound(hi))" }
        return nil
    }

    if field.control == .duration {
        let s = stringValue(value)
        if s.isEmpty { return "Enter a duration, e.g. 2m" }
        if !matches(durationRegex, s) { return "Use a duration like 2m, 90s, or 1h30m" }
        return nil
    }

    if let kind = field.valueKind, field.control == .text || field.control == .secret {
        let s = stringValue(value)
        if s.isEmpty { return field.optional ? nil : "This field is required" }
        switch kind {
        case .hostport:
            return validateHostPort(s)
        case .bytesize:
            // Docker-style size: 512m, 1g, 256k, 1073741824 (bytes), optional unit
            return matches(byteSizeRegex, s) ? nil : "Use a size like 512m, 1g, or 256k"
        case .cpu:
            if matches(cpuRegex, s), let n = Double(s), n > 0 { return nil }
            return "Use a positive number of CPUs, e.g. 1.0 or 0.5"
        case .hostname:
            return matches(hostnameRegex, s) ? nil : "Use a registry host like docker.io or ghcr.io"
        case .url:
            return matches(urlRegex, s) ? nil : "Use a URL starting with http:// or https://"
        case .secretkey:
            // A non-empty key must be reasonably strong. Empty is handled above
            // (api_key uses optional:true → blank keeps the current key).
            return s.count >= 16 ? nil : "API key must be at least 16 characters"
        }
    }

    // toggles / selects / multiselect have no value validation here.
    return nil
}

/// Format a numeric bound without a trailing ".0" for integer values, matching
/// how the TS template literal renders `${field.min}` / `${field.max}`.
private func formatBound(_ d: Double) -> String {
    if d == d.rounded() && abs(d) < 1e15 {
        return String(Int(d))
    }
    return String(d)
}
