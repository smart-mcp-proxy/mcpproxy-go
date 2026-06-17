package shellwrap

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// --- Unified login-shell environment capture ----------------------------
//
// captureLoginShellEnv sources the user's login shell exactly once per process
// and returns the full environment it exports. It is the single shell fork
// shared by LoginShellPATH (PATH-only view) and HydrateFromLoginShell (curated
// env merge), so startup never forks the login shell more than once.
//
// Why this exists: when mcpproxy is launched from a macOS GUI / launchd context
// (Launchpad, Dock, the SMAppService login item, or the tray spawning the
// core), the Go process inherits a launchd-minimal environment — it never
// sources the user's login shell, so it lacks Homebrew/Docker PATH entries and
// exported vars like DOCKER_HOST. Launched from a terminal it is healthy. See
// MCP-2751.

// osDarwin is the runtime.GOOS value for macOS (osWindows is defined in
// shellwrap.go).
const osDarwin = "darwin"

const (
	// loginShellEnvMarkerBegin / loginShellEnvMarkerEnd bracket the captured
	// `env -0` output inside the shell's stdout. The markers let us pluck the
	// real environment out of arbitrary banner / motd / oh-my-zsh / direnv
	// output that login-rc files routinely emit on stdout (see issue #439).
	// The strings are deliberately unlikely to appear in any rc-file banner.
	loginShellEnvMarkerBegin = "__MCPPROXY_LOGIN_ENV_BEGIN__"
	loginShellEnvMarkerEnd   = "__MCPPROXY_LOGIN_ENV_END__"
)

var (
	loginShellEnvOnce sync.Once
	loginShellEnvVal  map[string]string
)

// captureLoginShellEnv runs `<shell> -l -c 'env -0'` once, NUL-delimited so
// multiline / `=`-containing values survive, bracketed by unique markers, with
// a 5s timeout. The parsed environment is cached for the rest of the process
// lifetime. Returns nil on Windows or when the capture fails (the caller then
// falls back to the ambient process environment — no regression).
func captureLoginShellEnv(logger *zap.Logger) map[string]string {
	loginShellEnvOnce.Do(func() {
		loginShellEnvVal = doCaptureLoginShellEnv(logger)
	})
	return loginShellEnvVal
}

func doCaptureLoginShellEnv(logger *zap.Logger) map[string]string {
	if runtime.GOOS == osWindows {
		// Windows has no login shell; PATH lives in the registry and is handled
		// separately by secureenv. Consistent with LoginShellPATH returning "".
		return nil
	}

	shell := resolveLoginShell()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Bracket `env -0` with unique markers so rc-file banner output (welcome
	// messages, oh-my-zsh updates, direnv hooks, motd, etc.) does NOT leak into
	// the value we parse. We deliberately build the argv ourselves rather than
	// going through WrapWithUserShell because shellescape would quote the
	// command and suppress expansion.
	script := `printf '` + loginShellEnvMarkerBegin + `'; env -0; printf '` + loginShellEnvMarkerEnd + `'`
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		if logger != nil {
			logger.Debug("shellwrap: login-shell env capture failed",
				zap.String("shell", shell),
				zap.Error(err))
		}
		return nil
	}

	env := parseLoginShellEnv(string(out))
	if logger != nil {
		// Never log values — key count only.
		logger.Debug("shellwrap: captured login-shell env",
			zap.String("shell", shell),
			zap.Int("var_count", len(env)))
	}
	return env
}

// parseLoginShellEnv extracts the marker-bracketed region of the shell stdout
// and parses the NUL-delimited `env -0` dump into a map. It returns nil if the
// markers are absent or the body is not NUL-delimited (e.g. an `env` without
// `-0` support on a very old OS), so the caller degrades to the ambient env.
func parseLoginShellEnv(stdout string) map[string]string {
	i := strings.Index(stdout, loginShellEnvMarkerBegin)
	if i < 0 {
		return nil
	}
	body := stdout[i+len(loginShellEnvMarkerBegin):]
	if j := strings.Index(body, loginShellEnvMarkerEnd); j >= 0 {
		body = body[:j]
	}
	// env -0 emits NUL-separated records; absence of any NUL means `-0` was not
	// honored — refuse to misparse a newline-joined blob.
	if !strings.Contains(body, "\x00") {
		return nil
	}

	result := make(map[string]string, 48)
	for _, pair := range strings.Split(body, "\x00") {
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			continue // no key, or empty key
		}
		key := pair[:eq]
		if !isValidEnvKey(key) {
			continue
		}
		result[key] = pair[eq+1:]
	}
	return result
}

// isValidEnvKey guards against treating banner noise as an env record.
func isValidEnvKey(key string) bool {
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return key != ""
}

// resetLoginShellEnvCacheForTest clears the unified capture cache. Only
// referenced from tests in this package.
func resetLoginShellEnvCacheForTest() {
	loginShellEnvOnce = sync.Once{}
	loginShellEnvVal = nil
}

// --- Login-shell environment hydration -----------------------------------

// hydrationGOOS is a test seam so the macOS-only gate can be exercised on the
// Linux CI runners. Defaults to the real GOOS.
var hydrationGOOS = runtime.GOOS

// curatedSingleKeys is the allow-list of single-spelling environment variables
// hydrated from the login shell. It deliberately EXCLUDES secrets (AWS_*,
// GITHUB_TOKEN, ANTHROPIC_API_KEY, …): wholesale import would pull developer
// credentials into the daemon and every MCP subprocess. We import only PATH
// (handled separately), container, and tool-home configuration. HOME / USER /
// SHELL are never touched. Proxy vars are handled separately (proxyVarPairs)
// because they are case-insensitive to clients.
var curatedSingleKeys = []string{
	// Docker / container engine selection (supersedes the per-spawn capture in #699).
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
	// Tool-home roots that node/python version managers rely on.
	"NVM_DIR", "ASDF_DIR", "PYENV_ROOT", "VOLTA_HOME", "HOMEBREW_PREFIX", "COLIMA_HOME",
}

// proxyVarPairs groups each logical proxy variable's UPPER/lower spellings.
// HTTP clients (Go's net/http, curl, docker, …) honor EITHER spelling, so the
// two spellings are one logical var: if the operator set either one (even to
// empty, to disable an inherited proxy), hydrating the other spelling from the
// login shell would override that intent. We therefore skip BOTH spellings when
// EITHER is already present.
var proxyVarPairs = [][2]string{
	{"HTTP_PROXY", "http_proxy"},
	{"HTTPS_PROXY", "https_proxy"},
	{"NO_PROXY", "no_proxy"},
}

// HydrateFromLoginShell performs a one-time, allow-listed merge of the user's
// login-shell environment into the current process environment via os.Setenv,
// so that every downstream spawn path (docker lifecycle, stdio servers,
// uvx/npx, secureenv.BuildSecureEnvironment, ResolveDockerPath) inherits a
// correct PATH and curated vars with no call-site changes.
//
// It is a no-op (applied=false) unless mcpproxy was launched into a degraded
// environment: macOS only, AND the ambient PATH looks launchd-minimal (lacks
// /usr/local/bin and /opt/homebrew/bin). Terminal launches are left untouched.
//
// PATH is merged login-first (enriching, never shrinking). Curated keys are
// applied set-if-unset only — any operator-set value (including an explicitly
// empty value used to disable an inherited one) is never clobbered. The
// returned snapshot maps each applied key to its value for diagnostics; this
// function never logs values (key names + lengths only).
func HydrateFromLoginShell(logger *zap.Logger) (applied bool, snapshot map[string]string) {
	snapshot = make(map[string]string)

	if hydrationGOOS != osDarwin {
		// Linux LaunchAgent-equivalent is systemd (Environment=/EnvironmentFile=),
		// not shell sourcing. Windows uses the registry PATH and has no login
		// shell. Gate off both.
		return false, snapshot
	}
	if !looksLikeLaunchdMinimalPath(os.Getenv("PATH")) {
		// Healthy interactive launch — preserve the zero-cost / no-pollution
		// guarantee (mirrors secureenv TestLaunchdMinimalPath_AlreadyComprehensive).
		return false, snapshot
	}

	env := captureLoginShellEnv(logger)
	if len(env) == 0 {
		return false, snapshot
	}

	sep := string(os.PathListSeparator)

	// PATH: always merge login-first so docker / uvx / npx and their credential
	// helpers resolve. Enrich only — never shrink an already-broader PATH.
	if loginPath := env["PATH"]; loginPath != "" {
		current := os.Getenv("PATH")
		merged := mergePathUnique(loginPath, current, sep)
		if merged != current {
			_ = os.Setenv("PATH", merged)
			snapshot["PATH"] = merged
		}
	}

	// Single-spelling curated keys: set-if-unset only, never clobber an
	// operator-set value (LookupEnv, so an explicit empty value is preserved).
	for _, key := range curatedSingleKeys {
		setEnvIfUnset(env, key, snapshot)
	}

	// Proxy trio: alias-aware. If EITHER spelling is already present (even
	// intentionally empty, e.g. `https_proxy=` to disable), skip hydrating BOTH
	// — clients honor either spelling so importing the opposite case would
	// override operator intent.
	for _, pair := range proxyVarPairs {
		if _, up := os.LookupEnv(pair[0]); up {
			continue
		}
		if _, lo := os.LookupEnv(pair[1]); lo {
			continue
		}
		setEnvIfUnset(env, pair[0], snapshot)
		setEnvIfUnset(env, pair[1], snapshot)
	}

	if len(snapshot) == 0 {
		return false, snapshot
	}

	if logger != nil {
		logger.Info("shellwrap: hydrated login-shell environment for GUI/launchd launch",
			zap.Strings("keys", sortedKeys(snapshot)),
			zap.Int("count", len(snapshot)))
		for _, k := range sortedKeys(snapshot) {
			// Key name + value length only — never the value itself.
			logger.Debug("shellwrap: hydrated env var",
				zap.String("key", k),
				zap.Int("value_length", len(snapshot[k])))
		}
	}
	return true, snapshot
}

// setEnvIfUnset hydrates a single key from the captured login-shell env when it
// provides a non-empty value and the key is not already present in the process
// env. Records applied keys in snapshot. Uses LookupEnv so an explicitly
// set-empty operator value is preserved.
func setEnvIfUnset(loginEnv map[string]string, key string, snapshot map[string]string) {
	val, ok := loginEnv[key]
	if !ok || val == "" {
		return
	}
	if _, present := os.LookupEnv(key); present {
		return
	}
	_ = os.Setenv(key, val)
	snapshot[key] = val
}

// looksLikeLaunchdMinimalPath mirrors the secureenv heuristic
// (manager.go buildEnhancedPath): a PATH that already contains a common tool
// directory is a healthy interactive launch — hydration must be a no-op there.
func looksLikeLaunchdMinimalPath(path string) bool {
	for _, dir := range []string{"/usr/local/bin", "/opt/homebrew/bin"} {
		for _, p := range strings.Split(path, string(os.PathListSeparator)) {
			if p == dir {
				return false
			}
		}
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
