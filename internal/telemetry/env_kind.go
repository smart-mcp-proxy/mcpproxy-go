package telemetry

import (
	"os"
	"runtime"
	"sync"

	"golang.org/x/term"
)

// EnvKind classifies the process's runtime environment for retention telemetry
// (spec 044). The value is computed once at process startup from environment
// variables and filesystem probes, then cached for the lifetime of the
// process via DetectEnvKindOnce.
//
// Serialization: always lowercase string. An invalid / empty value indicates a
// programming error; the payload builder will reject it.
type EnvKind string

const (
	// EnvKindInteractive: real human on a desktop OS (macOS/Windows) or
	// Linux with DISPLAY/TTY present.
	EnvKindInteractive EnvKind = "interactive"

	// EnvKindCI: any known CI runner env var set (GitHub Actions, GitLab CI,
	// Jenkins, CircleCI, etc.).
	EnvKindCI EnvKind = "ci"

	// EnvKindCloudIDE: Codespaces, Gitpod, Replit, StackBlitz, Daytona, Coder.
	EnvKindCloudIDE EnvKind = "cloud_ide"

	// EnvKindContainer: /.dockerenv or /run/.containerenv present, or
	// $container env var set — with no CI/cloud-IDE markers.
	EnvKindContainer EnvKind = "container"

	// EnvKindHeadless: Linux with no DISPLAY/TTY and none of the above.
	EnvKindHeadless EnvKind = "headless"

	// EnvKindUnknown: classifier fell through all rules.
	EnvKindUnknown EnvKind = "unknown"
)

// AllEnvKinds returns the canonical ordered list of EnvKind values. Used by
// tests for exhaustiveness checks and by the payload builder for validation.
func AllEnvKinds() []EnvKind {
	return []EnvKind{
		EnvKindInteractive,
		EnvKindCI,
		EnvKindCloudIDE,
		EnvKindContainer,
		EnvKindHeadless,
		EnvKindUnknown,
	}
}

// IsValidEnvKind reports whether v is one of the canonical EnvKind constants.
func IsValidEnvKind(v EnvKind) bool {
	for _, k := range AllEnvKinds() {
		if k == v {
			return true
		}
	}
	return false
}

// FileProber abstracts filesystem existence checks so the env_kind detector
// can be unit-tested with a fake FS. Production uses defaultFileProber.
type FileProber interface {
	// Exists returns true if the file at path exists (any type: file, dir,
	// symlink). Errors other than "not exists" are treated as "exists" to
	// avoid mis-classifying a container due to permission issues.
	Exists(path string) bool
}

// TTYChecker abstracts stdin-is-a-terminal detection so the env_kind detector
// can be unit-tested without attaching a real TTY.
type TTYChecker interface {
	IsTerminal() bool
}

// defaultFileProber is the production FileProber. It wraps os.Stat.
type defaultFileProber struct{}

// Exists reports whether a file exists at path. Permission errors and other
// non-"does not exist" errors are treated as "exists" — see FileProber doc.
func (defaultFileProber) Exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}

// defaultTTYChecker wraps golang.org/x/term.IsTerminal(os.Stdin).
type defaultTTYChecker struct{}

// IsTerminal reports whether os.Stdin is attached to a terminal.
func (defaultTTYChecker) IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// ciEnvVars is the fixed set of env var names whose mere presence (any
// non-empty value) indicates a CI runner. Matches design §4.2 and research.md
// R1. Order is not significant.
var ciEnvVars = []string{
	"CI",
	"GITHUB_ACTIONS",
	"GITLAB_CI",
	"JENKINS_URL",
	"CIRCLECI",
	"BUILDKITE",
	"TF_BUILD",
	"TRAVIS",
	"DRONE",
	"BITBUCKET_BUILD_NUMBER",
	"TEAMCITY_VERSION",
	"APPVEYOR",
	"GITEA_ACTIONS",
}

// cloudIDEEnvVars is the fixed set of env var names whose presence indicates a
// cloud-IDE session.
var cloudIDEEnvVars = []string{
	"CODESPACES",
	"GITPOD_WORKSPACE_ID",
	"REPL_ID",
	"STACKBLITZ_ENV",
	"DAYTONA_WS_ID",
	"CODER_AGENT_TOKEN",
}

// hasAnyEnv returns true if any of names is present in env with a non-empty
// value.
func hasAnyEnv(env map[string]string, names []string) bool {
	for _, n := range names {
		if v, ok := env[n]; ok && v != "" {
			return true
		}
	}
	return false
}

// DetectEnvKind is the pure classifier that decides env_kind + env_markers
// from an injected environment map, filesystem prober, OS name, and TTY
// checker. Ordering is authoritative: cloud_ide wins over CI wins over
// container.
//
// NOTE: This ordering deviates from the original design doc (§4.2 / research.md
// R1) which put CI first. The change is motivated by gemini P1 cross-review:
// GitHub Codespaces and Gitpod routinely set CI=true alongside their cloud-IDE
// markers (CODESPACES, GITPOD_WORKSPACE_ID). Real humans in those ephemeral
// cloud sessions were being classified as `ci`, artificially deflating the
// Cloud IDE retention funnel. Prioritising cloud_ide over CI keeps interactive
// human sessions in the right bucket while still catching ordinary CI runners.
//
// Inputs:
//   - env: map of env-var name → value (caller builds this from os.Environ).
//   - fs: filesystem prober for /.dockerenv + /run/.containerenv.
//   - osName: runtime.GOOS value ("darwin", "linux", "windows", …).
//   - tty: stdin-is-a-TTY checker.
//
// Outputs:
//   - EnvKind: one of the canonical constants (never empty).
//   - EnvMarkers: booleans reflecting every observation feeding the decision,
//     including ones that were irrelevant to the final verdict (e.g. on CI a
//     container marker may still be true).
func DetectEnvKind(env map[string]string, fs FileProber, osName string, tty TTYChecker) (EnvKind, EnvMarkers) {
	// Compute all markers up front so every caller can see the complete
	// observation set, independent of which branch wins.
	markers := EnvMarkers{
		HasCIEnv:       hasAnyEnv(env, ciEnvVars),
		HasCloudIDEEnv: hasAnyEnv(env, cloudIDEEnvVars),
		HasTTY:         tty != nil && tty.IsTerminal(),
		HasDisplay:     env["DISPLAY"] != "" || env["WAYLAND_DISPLAY"] != "",
	}
	// Container marker: file presence OR $container env var in common set.
	if fs != nil {
		if fs.Exists("/.dockerenv") || fs.Exists("/run/.containerenv") {
			markers.IsContainer = true
		}
	}
	switch env["container"] {
	case "podman", "docker", "oci":
		markers.IsContainer = true
	}

	// Decision tree (first match wins). cloud_ide is checked BEFORE ci because
	// Codespaces / Gitpod set CI=true alongside their own markers — see
	// function doc comment.
	switch {
	case markers.HasCloudIDEEnv:
		return EnvKindCloudIDE, markers
	case markers.HasCIEnv:
		return EnvKindCI, markers
	case markers.IsContainer:
		return EnvKindContainer, markers
	}

	switch osName {
	case "darwin", "windows":
		return EnvKindInteractive, markers
	case "linux":
		if markers.HasDisplay || markers.HasTTY {
			return EnvKindInteractive, markers
		}
		return EnvKindHeadless, markers
	}
	return EnvKindUnknown, markers
}

// envKindOnce guards the cached DetectEnvKindOnce result.
var (
	envKindOnce    sync.Once
	envKindCached  EnvKind
	envMarkerCache EnvMarkers
)

// DetectEnvKindOnce runs the real detector against os.Environ / runtime.GOOS /
// os.Stdin exactly once per process lifetime and caches the result. Subsequent
// callers get the cached value. Safe for concurrent use.
func DetectEnvKindOnce() (EnvKind, EnvMarkers) {
	envKindOnce.Do(func() {
		envKindCached, envMarkerCache = DetectEnvKind(
			envMap(), defaultFileProber{}, runtime.GOOS, defaultTTYChecker{},
		)
	})
	return envKindCached, envMarkerCache
}

// DetectEnvKindOnceWith is the test-only entry point: it runs the given
// detector function at most once per reset cycle and caches the result.
// Production code should always use DetectEnvKindOnce. This helper exists so
// the concurrency test can inject a counting fake detector.
func DetectEnvKindOnceWith(detector func() (EnvKind, EnvMarkers)) (EnvKind, EnvMarkers) {
	envKindOnce.Do(func() {
		envKindCached, envMarkerCache = detector()
	})
	return envKindCached, envMarkerCache
}

// ResetEnvKindForTest resets the cached env_kind result so tests can rerun
// DetectEnvKindOnce with fresh inputs. MUST NOT be called from production code
// — calling it would re-detect on config reload and potentially re-classify a
// CI runner that set env vars late. Guard calls behind _test.go files only.
func ResetEnvKindForTest() {
	envKindOnce = sync.Once{}
	envKindCached = ""
	envMarkerCache = EnvMarkers{}
}

// envMap reads the process environment into a map[string]string. Used once at
// startup inside DetectEnvKindOnce.
func envMap() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		// Split on the first "=". Values may contain "=", keys never do.
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
