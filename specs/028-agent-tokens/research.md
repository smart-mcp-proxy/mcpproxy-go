# Research: Agent Tokens

## Decision 1: Token Hashing Strategy

**Decision**: HMAC-SHA256 with a server-side key stored in OS keyring.

**Rationale**: Provides O(1) lookup (compute HMAC, look up in BBolt by hash) while remaining secure. Bcrypt is designed for passwords (slow by design) and requires prefix-based candidate search — unnecessary complexity for <100 tokens. HMAC-SHA256 is fast, deterministic, and secure as long as the key is protected.

**Alternatives considered**:
- **Bcrypt**: Slow lookup (must iterate candidates), designed for password hashing where brute-force resistance matters. Agent tokens have 256 bits of entropy — brute force is already infeasible.
- **SHA256 (no key)**: Vulnerable if database is leaked — attacker could compute hashes of stolen tokens. HMAC adds key material so database leak alone is insufficient.
- **Argon2**: Same drawbacks as bcrypt (slow, candidate iteration), more complex dependency.

## Decision 2: Token Storage Location

**Decision**: New `agent_tokens` bucket in existing BBolt database (`config.db`).

**Rationale**: BBolt is already a core dependency, transactions are ACID, and the token count is small (<100). No new infrastructure needed.

**Alternatives considered**:
- **Separate SQLite database**: Adds a dependency, no benefit at this scale.
- **Config file JSON**: Not appropriate for runtime-created credentials with hashed secrets.
- **In-memory with file backup**: Loses ACID guarantees, adds complexity.

## Decision 3: HMAC Key Storage

**Decision**: Generate HMAC key on first token creation, store in OS keyring (macOS Keychain, Linux secret-service, Windows Credential Manager) via existing `go-keyring` dependency.

**Rationale**: MCPProxy already uses the keyring for secrets management. The HMAC key is a server-side secret that should not be stored alongside the hashed tokens in BBolt.

**Fallback**: If keyring is unavailable (headless Linux), derive key from a file at `~/.mcpproxy/.token_key` with 0600 permissions. This mirrors how SSH handles key files.

## Decision 4: Auth Context Propagation

**Decision**: Use Go `context.Context` with typed key to propagate `AuthContext` through request handlers.

**Rationale**: Standard Go pattern. The auth middleware sets `AuthContext` on the context, and MCP handlers extract it to enforce scoping. This avoids passing auth state through function parameters.

**Implementation**: `context.WithValue(ctx, authContextKey, authCtx)` in middleware, `AuthContextFromContext(ctx)` helper in handlers.

## Decision 5: MCP Endpoint Scoping

**Decision**: Scope enforcement at two points: (1) `handleRetrieveTools` filters search results, (2) `handleCallToolVariant` checks server name before proxying.

**Rationale**: These are the only two entry points for tool operations. Filtering at search prevents tool discovery outside scope. Filtering at call prevents execution outside scope. Defense in depth.

**Implementation detail**: For `retrieve_tools`, filter the `results` slice after `p.index.Search()` by checking each result's server name against `AuthContext.AllowedServers`. For `call_tool_*`, check `serverName` (already parsed at line 1033-1038 in mcp.go) against allowed servers, and check `toolVariant` against allowed permissions.

## Decision 6: Web UI Framework

**Decision**: Add `AgentTokens.vue` view in existing Vue 3 + DaisyUI frontend.

**Rationale**: Follows existing patterns. The frontend already has server management views, activity views, etc. Agent token management is a natural addition.
