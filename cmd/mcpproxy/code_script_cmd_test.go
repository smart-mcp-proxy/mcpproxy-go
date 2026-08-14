package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/codescripts"

	"go.uber.org/zap"
)

// TestValidateCodeSourceFlags (Spec 097, T006) pins the three-way exclusion:
// exactly one of --code, --file or --script names the source to run.
func TestValidateCodeSourceFlags(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		file    string
		script  string
		wantErr string
	}{
		{name: "code alone", code: "({})"},
		{name: "file alone", file: "script.js"},
		{name: "script alone", script: "daily-report"},
		{
			name:    "nothing at all",
			wantErr: "one of --code, --file or --script",
		},
		{
			name:    "code and file",
			code:    "({})",
			file:    "script.js",
			wantErr: "mutually exclusive",
		},
		{
			name:    "script and code",
			code:    "({})",
			script:  "daily-report",
			wantErr: "mutually exclusive",
		},
		{
			name:    "script and file",
			file:    "script.js",
			script:  "daily-report",
			wantErr: "mutually exclusive",
		},
		{
			name:    "all three",
			code:    "({})",
			file:    "script.js",
			script:  "daily-report",
			wantErr: "mutually exclusive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCodeSourceFlags(tc.code, tc.file, tc.script)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCodeSourceFlags(%q, %q, %q) = %v, want nil", tc.code, tc.file, tc.script, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateCodeSourceFlags(%q, %q, %q) = nil, want an error mentioning %q", tc.code, tc.file, tc.script, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestCodeExecLanguageArg pins that --language reaches the daemon ONLY when the
// user actually set it. The flag's own default ("javascript") must never travel
// as an explicit choice: against a stored .ts script it would fake a
// contradiction and turn every TypeScript script into an error.
func TestCodeExecLanguageArg(t *testing.T) {
	previousValue, previousExplicit := codeLanguage, codeLanguageExplicit
	t.Cleanup(func() { codeLanguage, codeLanguageExplicit = previousValue, previousExplicit })

	codeLanguage, codeLanguageExplicit = "javascript", false
	if got := codeExecLanguageArg(); got != "" {
		t.Fatalf("codeExecLanguageArg() = %q with the flag untouched, want empty", got)
	}

	codeLanguage, codeLanguageExplicit = "typescript", true
	if got := codeExecLanguageArg(); got != "typescript" {
		t.Fatalf("codeExecLanguageArg() = %q, want %q", got, "typescript")
	}

	// An explicitly requested "javascript" is a real choice and must travel, so
	// a .ts stored script reports the contradiction instead of hiding it.
	codeLanguage, codeLanguageExplicit = "javascript", true
	if got := codeExecLanguageArg(); got != "javascript" {
		t.Fatalf("codeExecLanguageArg() = %q for an explicit --language javascript, want it forwarded", got)
	}
}

// TestSetCodeLanguageExplicit pins the wiring between the cobra flag and the
// value codeExecLanguageArg reports.
func TestSetCodeLanguageExplicit(t *testing.T) {
	previous := codeLanguageExplicit
	flag := codeExecCmd.Flags().Lookup("language")
	if flag == nil {
		t.Fatal("code exec has no --language flag")
	}
	previousChanged, previousValue := flag.Changed, codeLanguage
	t.Cleanup(func() {
		codeLanguageExplicit = previous
		flag.Changed = previousChanged
		codeLanguage = previousValue
		_ = codeExecCmd.Flags().Set("language", previousValue)
		flag.Changed = previousChanged
	})

	flag.Changed = false
	setCodeLanguageExplicit(codeExecCmd)
	if codeLanguageExplicit {
		t.Fatal("an untouched --language must not count as explicit")
	}

	if err := codeExecCmd.Flags().Set("language", "typescript"); err != nil {
		t.Fatalf("failed to set --language: %v", err)
	}
	setCodeLanguageExplicit(codeExecCmd)
	if !codeLanguageExplicit {
		t.Fatal("a --language the user set must count as explicit")
	}
}

// TestCodeExecToolArgs pins what standalone (in-process) mode hands the
// code_execution tool: for a stored script the NAME, never content the CLI
// resolved itself — the handler is the only execution-time resolver on every
// surface.
func TestCodeExecToolArgs(t *testing.T) {
	previousValue, previousExplicit := codeLanguage, codeLanguageExplicit
	t.Cleanup(func() { codeLanguage, codeLanguageExplicit = previousValue, previousExplicit })
	codeLanguage, codeLanguageExplicit = "javascript", false

	input := map[string]interface{}{"value": 21}

	t.Run("stored script sends the name only", func(t *testing.T) {
		args := codeExecToolArgs("", "daily-report", input)
		if args["script"] != "daily-report" {
			t.Fatalf("args[script] = %v, want %q", args["script"], "daily-report")
		}
		if _, present := args["code"]; present {
			t.Fatalf("a stored-script invocation must send no code: %v", args)
		}
		if _, present := args["language"]; present {
			t.Fatalf("an untouched --language must not be sent: %v", args)
		}
	})

	t.Run("inline code sends the source", func(t *testing.T) {
		args := codeExecToolArgs("({result: 1})", "", input)
		if args["code"] != "({result: 1})" {
			t.Fatalf("args[code] = %v", args["code"])
		}
		if _, present := args["script"]; present {
			t.Fatalf("an inline invocation must send no script name: %v", args)
		}
	})

	t.Run("an explicit language travels", func(t *testing.T) {
		codeLanguage, codeLanguageExplicit = "typescript", true
		args := codeExecToolArgs("const x: number = 1", "", input)
		if args["language"] != "typescript" {
			t.Fatalf("args[language] = %v, want typescript", args["language"])
		}
	})
}

const (
	codeScriptDaemonChildEnv   = "MCPPROXY_TEST_CODE_SCRIPT_CHILD"
	codeScriptFallbackChildEnv = "MCPPROXY_TEST_CODE_SCRIPT_FALLBACK_CHILD"
)

// TestRunCodeExecClientMode_SendsScriptName (T006) drives the real daemon-mode
// path with --script and asserts on the request the daemon actually receives:
// the script NAME and no source. The command path calls os.Exit on failure, so
// it runs in a child process.
func TestRunCodeExecClientMode_SendsScriptName(t *testing.T) {
	if os.Getenv(codeScriptDaemonChildEnv) == "1" {
		codeTimeout = 60000
		codeMaxToolCalls = 0
		codeAllowedSrvs = nil
		codeLanguage = "javascript"
		codeLanguageExplicit = false
		codeScriptName = "daily-report"
		useMissingCodeConfig(t)

		client := cliclient.NewClientWithAPIKey(os.Getenv(codeScriptDaemonChildEnv+"_ENDPOINT"), "", nil)
		if err := runCodeExecClientMode(client, "", map[string]interface{}{"value": 1}, zap.NewNop()); err != nil {
			t.Fatalf("runCodeExecClientMode returned error: %v", err)
		}
		return
	}

	type capture struct {
		body map[string]interface{}
	}
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case "/api/v1/code/exec":
			_ = json.NewDecoder(r.Body).Decode(&got.body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": map[string]interface{}{"value": 1}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeExecClientMode_SendsScriptName$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(),
		codeScriptDaemonChildEnv+"=1",
		codeScriptDaemonChildEnv+"_ENDPOINT="+srv.URL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon-mode stored-script exec failed (%v)\nchild output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "Using daemon mode") {
		t.Fatalf("child never took the daemon path\nchild output:\n%s", out)
	}
	if got.body == nil {
		t.Fatalf("the daemon never received a code execution request\nchild output:\n%s", out)
	}
	if got.body["script"] != "daily-report" {
		t.Fatalf("request body script = %v, want %q (body: %v)", got.body["script"], "daily-report", got.body)
	}
	if code, present := got.body["code"]; present && code != "" {
		t.Fatalf("daemon mode must send the script name, not its content (code=%q)", code)
	}
	if _, present := got.body["language"]; present {
		t.Fatalf("an untouched --language must not be sent: %v", got.body)
	}
}

// TestRunCodeExecClientMode_ScriptSurvivesPingFallback (T006) pins that a dead
// daemon does not change what --script means: the standalone fallback runs the
// SAME name through the in-process handler, whose authority is the shared
// config-path helper.
func TestRunCodeExecClientMode_ScriptSurvivesPingFallback(t *testing.T) {
	if os.Getenv(codeScriptFallbackChildEnv) == "1" {
		codeTimeout = 20000
		codeMaxToolCalls = 0
		codeAllowedSrvs = nil
		codeLanguage = "javascript"
		codeLanguageExplicit = false
		codeScriptName = "fallback"

		dir := os.Getenv(codeScriptFallbackChildEnv + "_DIR")
		codeConfigPath = filepath.Join(dir, "mcp_config.json")
		t.Cleanup(func() { codeConfigPath = "" })

		// Port 1 refuses connections immediately, so the ping fails fast.
		client := cliclient.NewClientWithAPIKey("http://127.0.0.1:1", "", nil)
		_ = runCodeExecClientMode(client, "", map[string]interface{}{"value": 20}, zap.NewNop())
		return
	}

	dir := t.TempDir()
	cfg := map[string]interface{}{
		"listen":                   "127.0.0.1:0",
		"data_dir":                 dir,
		"enable_code_execution":    true,
		"code_execution_pool_size": 1,
	}
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_config.json"), cfgBytes, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "fallback.js"), []byte("({result: input.value + 1})"), 0o600); err != nil {
		t.Fatalf("failed to write stored script: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeExecClientMode_ScriptSurvivesPingFallback$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		codeScriptFallbackChildEnv+"=1",
		codeScriptFallbackChildEnv+"_DIR="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("standalone fallback failed for a stored script (%v)\nchild output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "standalone mode") {
		t.Fatalf("child never fell back to standalone mode\nchild output:\n%s", out)
	}
	if !strings.Contains(string(out), `"result": 21`) {
		t.Fatalf("the stored script did not run through the in-process handler\nchild output:\n%s", out)
	}
}

// TestLocalCodeScripts (T010) pins the daemonless listing: it reads the scripts
// directory implied by the config FILE the code command works against, and
// reports every entry with its status.
func TestLocalCodeScripts(t *testing.T) {
	dir := t.TempDir()
	previous := codeConfigPath
	codeConfigPath = filepath.Join(dir, "mcp_config.json")
	t.Cleanup(func() { codeConfigPath = previous })

	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "alpha.js"), []byte("({a: 1})"), 0o600); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "blank.ts"), nil, 0o600); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	gotDir, entries, err := localCodeScripts()
	if err != nil {
		t.Fatalf("localCodeScripts returned error: %v", err)
	}
	if gotDir != scriptsDir {
		t.Fatalf("dir = %q, want %q", gotDir, scriptsDir)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].Name != "alpha" || entries[0].Status != codescripts.StatusOK {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].Name != "blank" || entries[1].Status != codescripts.StatusInvalid || entries[1].Reason != codescripts.ReasonEmpty {
		t.Fatalf("entries[1] = %+v", entries[1])
	}
}

// TestRenderCodeScripts pins the human-readable listing: the directory that was
// read, every name, and the reason an unusable entry cannot be invoked.
func TestRenderCodeScripts(t *testing.T) {
	var out strings.Builder
	renderCodeScripts(&out, "/cfg/scripts", []codescripts.Entry{
		{Name: "alpha", Paths: []string{"/cfg/scripts/alpha.js"}, Status: codescripts.StatusOK},
		{Name: "blank", Paths: []string{"/cfg/scripts/blank.js"}, Status: codescripts.StatusInvalid, Reason: codescripts.ReasonEmpty},
	})
	text := out.String()
	for _, want := range []string{"/cfg/scripts", "alpha", "blank", codescripts.ReasonEmpty} {
		if !strings.Contains(text, want) {
			t.Fatalf("listing does not mention %q:\n%s", want, text)
		}
	}

	var empty strings.Builder
	renderCodeScripts(&empty, "/cfg/scripts", nil)
	if !strings.Contains(empty.String(), "/cfg/scripts") {
		t.Fatalf("an empty listing must still name the directory searched:\n%s", empty.String())
	}
}

const codeScriptsListChildEnv = "MCPPROXY_TEST_CODE_SCRIPTS_LIST_CHILD"

// TestRunCodeScriptsList_DaemonPath (T010) drives `code scripts list` against a
// running daemon: the daemon's answer is what the user sees, not a local
// re-listing that could disagree with the process that actually executes.
func TestRunCodeScriptsList_DaemonPath(t *testing.T) {
	if os.Getenv(codeScriptsListChildEnv) == "1" {
		dir := os.Getenv(codeScriptsListChildEnv + "_DIR")
		codeConfigPath = filepath.Join(dir, "mcp_config.json")
		t.Cleanup(func() { codeConfigPath = "" })
		if err := runCodeScriptsList(codeScriptsListCmd, nil); err != nil {
			t.Fatalf("runCodeScriptsList returned error: %v", err)
		}
		return
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case "/api/v1/code/scripts":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"dir": "/daemon/scripts",
					"scripts": []map[string]interface{}{
						{"name": "from-daemon", "paths": []string{"/daemon/scripts/from-daemon.js"}, "status": "ok"},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// The local scripts directory holds a DIFFERENT name, so a listing that
	// quietly resolved locally instead of asking the daemon is visible.
	dir := t.TempDir()
	cfg, err := json.Marshal(map[string]interface{}{"listen": "127.0.0.1:0", "data_dir": dir})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_config.json"), cfg, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "only-local.js"), []byte("1"), 0o600); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeScriptsList_DaemonPath$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(),
		codeScriptsListChildEnv+"=1",
		codeScriptsListChildEnv+"_DIR="+dir,
		"MCPPROXY_TRAY_ENDPOINT="+srv.URL,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("code scripts list failed against a daemon (%v)\nchild output:\n%s", err, out)
	}
	if !strings.Contains(string(out), "from-daemon") {
		t.Fatalf("the listing did not come from the daemon\nchild output:\n%s", out)
	}
	if strings.Contains(string(out), "only-local") {
		t.Fatalf("the listing was resolved locally while a daemon was running\nchild output:\n%s", out)
	}
}

const codeScriptNotFoundChildEnv = "MCPPROXY_TEST_CODE_SCRIPT_NOTFOUND_CHILD"

// TestRunCodeExecStandalone_ScriptNotFoundReportsAvailable pins that the
// not-found error reaches the user in standalone mode. That error IS the
// discovery mechanism (FR-004): it carries the available script names, so
// reporting "unexpected result format" instead of the tool's own text leaves a
// mistyped name with no way back.
func TestRunCodeExecStandalone_ScriptNotFoundReportsAvailable(t *testing.T) {
	if os.Getenv(codeScriptNotFoundChildEnv) == "1" {
		codeTimeout = 20000
		codeMaxToolCalls = 0
		codeAllowedSrvs = nil
		codeLanguage = "javascript"
		codeLanguageExplicit = false
		codeScriptName = "nope"
		codeConfigPath = filepath.Join(os.Getenv(codeScriptNotFoundChildEnv+"_DIR"), "mcp_config.json")

		cfg, err := loadCodeConfig()
		if err != nil {
			t.Fatalf("failed to load config: %v", err)
		}
		_ = runCodeExecStandalone(cfg, "", map[string]interface{}{}, zap.NewNop())
		return
	}

	dir := t.TempDir()
	cfg, err := json.Marshal(map[string]interface{}{
		"listen":                   "127.0.0.1:0",
		"data_dir":                 dir,
		"enable_code_execution":    true,
		"code_execution_pool_size": 1,
	})
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_config.json"), cfg, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "double.js"), []byte("({result: 1})"), 0o600); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestRunCodeExecStandalone_ScriptNotFoundReportsAvailable$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		codeScriptNotFoundChildEnv+"=1",
		codeScriptNotFoundChildEnv+"_DIR="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("naming a script that does not exist must fail\nchild output:\n%s", out)
	}
	if !strings.Contains(string(out), "double") {
		t.Fatalf("the not-found error must list the available scripts\nchild output:\n%s", out)
	}
}
