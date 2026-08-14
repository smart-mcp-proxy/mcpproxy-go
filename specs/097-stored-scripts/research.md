# Research: Server-Side Stored Scripts (Spec 097)

Verified on branch `097-stored-scripts` (stacked on 096). File:line references to that state.

## R1. Confined open — os.Root, with a symlink correction

**Decision**: resolve via `os.OpenRoot(scriptsDir)` → `root.Lstat(name)` reject non-regular → `root.Open(name)` → `f.Stat()` for the 256KB bound → bounded read.

**Rationale**: go.mod declares go 1.25.5 (os.Root available; zero current uses in repo — all grep hits are .gomodcache noise, scope searches to internal/ cmd/). **os.Root follows in-root symlinks by design**, and caller-supplied `O_NOFOLLOW` is silently defeated (Go passes it itself and transparently re-resolves ELOOP up to 8 links). So the symlink-rejection policy comes from `root.Lstat` (AT_SYMLINK_NOFOLLOW on the final component) + `Mode().IsRegular()`. The Lstat→Open window is acceptable and must be stated in review terms: **escape is prevented by os.Root at open time by construction**; the regular-file check is policy — the residual race can only substitute one in-root file for another, never escape. `Root.ReadFile` rejected (no type check, no size bound).

## R2. Active-config-file authority — three resolvers, two traps

- Daemon: `server.go:2827 GetConfigPath()` → runtime configSvc path, falls back to `config.GetConfigPath(DataDir)` (`loader.go:426`, defaults to `~/.mcpproxy/mcp_config.json`) — never empty in daemon mode. Scripts dir = `filepath.Join(filepath.Dir(path), "scripts")`.
- **Trap 1**: `mcp_code_execution.go:132-135` — configPath is read only when `p.mainServer != nil`; CLI in-process mode yields `""`. The scripts-dir helper must fall back to `config.GetConfigPath(cfg.DataDir)` when mainServer is nil, and script resolution must be hoisted above the current code-requirement at `:79`.
- **Trap 2**: CLI standalone `loadCodeConfig()` (`code_cmd.go:415-446`) computes the config file path as a local and never returns it; extract a shared `codeConfigFilePath()` helper (honors `--config`, defaults `~/.mcpproxy/mcp_config.json`). Do NOT derive from DataDir (the `--data-dir` override at :443 mutates after path resolution and would disagree with FR-001).

## R3. Arg seam — one handler, THREE registration sites

- Handler: `mcp_code_execution.go:79` RequireString("code") → becomes optional + XOR with `script` (args map already at :84).
- Registrations to edit: `mcp.go:912-933`, `mcp_routing.go:506-529`, **and the disabled stub `mcp_routing.go:487-496`** (has its own inline description). `mcp.Required()` must drop from `code` in the live sites (schema can't express XOR; handler enforces). New shared `codeExecutionScriptDescription` constant beside the 096 description constants (`mcp_code_execution.go:28-66`).
- REST: `CodeExecRequest` gains `Script string` (`code_exec.go:23-28`); XOR pre-validated at REST for a proper 400 (file convention, comment at :98-101); forwarding is the plain args map at :150-154.

## R4. CLI — ordering inversion and the options seam

- Flags at `code_cmd.go:82-91`; three-way mutual exclusion (`--code`/`--file`/`--script`) in `runCodeExec` :121-129.
- **Ordering trap**: `loadCodeAndInput()` runs at :131 but `loadCodeConfig()` at :143 — script resolution needs the config path, so standalone resolution defers out of `loadCodeAndInput`; daemon mode sends the NAME (never content) via `cliclient.CodeExecOptions` (`client.go:227-229`, currently only `Language`) — add `Script string`, body set beside language at :253-255; positional signatures untouched. Ping-failure fallback (:189-206) re-resolves standalone — must work there too.
- `code scripts list`: new `codeScriptsCmd` with `list` child, `codeCmd.AddCommand` beside :79. Daemon running → GET /api/v1/code/scripts; else local resolution.

## R5. REST listing — zero interface change

Register `r.Get("/code/scripts", s.handleListScripts)` beside `server.go:843`; inherits auth/timeout/telemetry from the /api/v1 block (:690-696); no long-running budget entry needed. **`GetConfigPath()` is already on ServerController (`server.go:139`)** — compute the dir from it; do NOT add an interface method (five hand-written mocks would need updating). Envelope: `s.writeSuccess` / `contracts.NewSuccessResponse` ({success,data} — not code_exec's bespoke {ok,...}). Swagger godoc per `handleGetDockerStatus` pattern (`server.go:5161-5170`) + `make swagger` (CI enforces swagger-verify).

## R6. Activity/history — purely additive

Both payloads are free-form maps: `codeExecRecord.Arguments` (`mcp_code_execution.go:348-352`) and `codeExecArgs` (:404-408) get `"script": name` added; `code` keeps the resolved source (Spec 024 parity). Token accounting unaffected (keys off result).

## R7. TypeScript — flows unchanged; contradiction check server-side

Setting `options.Language = "typescript"` suffices (transpile at `runtime.go:178-184`). `ValidateLanguage` accepts "" so it cannot detect contradictions — the extension-vs-explicit-language check lives where `effectiveLanguage` is computed (`mcp_code_execution.go:201-203`), after script resolution; deriving effectiveLanguage from the extension also keeps activity records honest.

## R8. Tests landscape

- REST: `internal/httpapi/code_exec_test.go` mockController + `TestCodeExecHandler_MissingCode` (XOR template), `TestCodeExecHandler_InvalidLanguage` (contradiction 400).
- Args forwarding: `codeExecArgsCapture` at `internal/server/code_execution_options_test.go:106-112` — assert daemon mode sends `script`, not content.
- CLI: `code_cmd_test.go` child re-exec pattern (:108,:144) incl. ping-failure fallback (:129).
- E2E: `/code/exec` covered in Go e2e tests; **`scripts/test-api-e2e.sh` has no code-exec coverage** — extending it is optional, note honestly.
- SC-003 proof: split the name validator so it is independently callable; table-test the traversal corpus against it (no fs hook needed). **Symlink test cases must be guarded `runtime.GOOS != "windows"`** (platform divergence; repo precedent for silently-vanishing tests).
