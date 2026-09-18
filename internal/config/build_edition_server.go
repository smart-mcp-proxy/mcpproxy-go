//go:build server

package config

// isServerEditionBuild is true when the binary is compiled with the `server`
// build tag (the mcpproxy-server binary), independent of whether the
// server_edition.* config block is enabled. Spec 107 FR-014: the audit_log
// per-edition default is keyed on the BUILD, not the feature flag, because
// the audit funnels themselves are edition-neutral code that runs in every
// server-edition binary.
const isServerEditionBuild = true
