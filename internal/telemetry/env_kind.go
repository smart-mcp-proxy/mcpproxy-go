package telemetry

// EnvKind classifies the process's runtime environment for retention telemetry
// (spec 044). The value is computed once at process startup from environment
// variables and filesystem probes, then cached for the lifetime of the
// process via DetectEnvKindOnce (added in a later task).
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
