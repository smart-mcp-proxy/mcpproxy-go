package telemetry

// EnvMarkers carries the raw boolean observations feeding the EnvKind decision
// tree (spec 044). All fields MUST remain Go bool values: the anonymity
// scanner re-asserts this on the serialized JSON to prevent a future refactor
// from accidentally widening any of these to a string or number.
//
// Only booleans are allowed here. Do NOT add fields that carry env-var values,
// hostnames, usernames, or paths — those are PII by policy.
type EnvMarkers struct {
	// HasCIEnv is true when any known CI env var is set (e.g., CI=true,
	// GITHUB_ACTIONS, GITLAB_CI, JENKINS_URL, CIRCLECI).
	HasCIEnv bool `json:"has_ci_env"`

	// HasCloudIDEEnv is true when a cloud-IDE env var is set (e.g.,
	// CODESPACES, GITPOD_WORKSPACE_ID, REPL_ID, STACKBLITZ_WORKER,
	// DAYTONA_WORKSPACE_ID, CODER_AGENT_TOKEN).
	HasCloudIDEEnv bool `json:"has_cloud_ide_env"`

	// IsContainer is true when /.dockerenv or /run/.containerenv exists, or
	// the $container env var is set.
	IsContainer bool `json:"is_container"`

	// HasTTY is true when stdin is attached to a terminal.
	HasTTY bool `json:"has_tty"`

	// HasDisplay is true when DISPLAY or WAYLAND_DISPLAY env var is set
	// (Linux desktop session indicator).
	HasDisplay bool `json:"has_display"`
}
