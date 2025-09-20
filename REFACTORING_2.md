# REFACTORING.md


---

## P8-SEC — Secrets Storage & UX (Issue #58)

**Context & Motivation**  
Right now secrets live in plaintext config. We’ll resolve secrets via a provider chain and store securely in OS keyrings first. Key library: **zalando/go-keyring** (simple, OS-agnostic; used by chezmoi).

**Plan**  
1) Add a **SecretRef** syntax to config strings: `${env:NAME}`, `${keyring:alias}`, `${op:vault/item#field}`, `${age:/path/to/file.age}`.  
2) Implement `internal/secret/resolver` with providers: **Env**, **Keyring**.  
3) CLI: `mcpproxy secrets set|get|del|list` (Keyring), `mcpproxy secrets migrate --from=plaintext --to=keyring`.  
4) REST (admin-only, optional): `/api/v1/secrets/refs` (list refs, masked), `/api/v1/secrets/migrate` (dry-run). No endpoints return secret values.  
5) Web UI page “Secrets”: show unresolved refs, buttons to **Store in Keychain**.  
6) Tray UX: when a secret is missing, show a badge + menu item to open Secrets page.
7) User can set secrets via CLI, REST, or Web UI.
8) If user have many secrets, app shoudl ask for password/fingerprint only once. 
9) Update docs to reflect the new secret storage.

**Verification**  
- Unit: resolver tests for each provider; golden tests for config expansion.  
- Integration: on macOS, saving to Keychain prompt; on Linux, Secret Service collection `login`; on Windows, WinCred entry created.  
- E2E: run tool invoking `${keyring:github_token}`; confirm no plaintext in config or logs.

**Exit Criteria**  
- No plaintext secrets in config or logs.  
- Config with SecretRef strings resolves at runtime across platforms.  
- CLI migration moves detected plaintext to keyring and rewrites config to refs.

**Rollback**  
Disable secret resolution with `--secrets=off`; leave plaintext config (not recommended).

