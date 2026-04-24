# Quickstart — Diagnostics & Error Taxonomy

## For developers: adding a new error code in 5 steps

1. **Pick a code name** following `MCPX_<DOMAIN>_<SPECIFIC>`. Example: `MCPX_HTTP_CERT_EXPIRED`.
2. **Add the constant** to `internal/diagnostics/codes.go`:

   ```go
   const HTTPCertExpired Code = "MCPX_HTTP_CERT_EXPIRED"
   ```

3. **Register the catalog entry** in `internal/diagnostics/registry.go`:

   ```go
   registry[HTTPCertExpired] = CatalogEntry{
       Code:     HTTPCertExpired,
       Severity: SeverityError,
       UserMessage: "Server TLS certificate has expired.",
       FixSteps: []FixStep{
           {Type: FixStepLink, Label: "Contact server administrator",
             URL: "docs/errors/MCPX_HTTP_CERT_EXPIRED.md"},
       },
       DocsURL: "docs/errors/MCPX_HTTP_CERT_EXPIRED.md",
   }
   ```

4. **Classify the error** in `internal/diagnostics/classifier.go`:

   ```go
   var expired x509.CertificateInvalidError
   if errors.As(err, &expired) && expired.Reason == x509.Expired {
       return HTTPCertExpired
   }
   ```

5. **Create the docs page** at `docs/errors/MCPX_HTTP_CERT_EXPIRED.md` with cause, symptoms, fix steps. Run `./scripts/check-errors-docs-links.sh` to verify bidirectional linkage. Run `go test ./internal/diagnostics/...` to confirm catalog completeness.

## For users: diagnosing a failing server

### From the web UI

1. Open `http://127.0.0.1:8080/ui/` (or via tray menu).
2. Click on a failing server in the list.
3. The ErrorPanel shows the code, explanation, and fix buttons.
4. Click a fix button. Destructive actions show a dry-run preview first.

### From the CLI

```bash
# Show diagnostics for a single server
mcpproxy doctor --server github-server

# List all codes in the catalog
mcpproxy doctor list-codes

# Run the registered fix for a specific code (dry-run default for destructive)
mcpproxy doctor fix MCPX_STDIO_SPAWN_ENOENT --server github-server

# Execute the fix for real (destructive fixes only)
mcpproxy doctor fix MCPX_OAUTH_REFRESH_EXPIRED --server github-server --execute
```

### From the macOS tray

- A red badge on the tray icon indicates at least one server in error state.
- Open the tray menu; the "Fix Issues (N)" group lists failing servers.
- Click a server entry; the web UI opens at that server's detail page with the ErrorPanel visible.

## E2E smoke walkthrough

```bash
cd /Users/user/repos/mcpproxy-go-diagnostics-taxonomy
./scripts/test-diagnostics-e2e.sh   # configures a broken stdio server, asserts MCPX_STDIO_SPAWN_ENOENT
./scripts/test-api-e2e.sh           # full API E2E — should remain green
```

Expected output contains:

```
[PASS] GET /api/v1/servers/broken/diagnostics returned error_code = "MCPX_STDIO_SPAWN_ENOENT"
[PASS] POST /api/v1/diagnostics/fix with mode=dry_run returned outcome=success + preview
[PASS] Rate limit: second immediate POST returned 429 with Retry-After
```
