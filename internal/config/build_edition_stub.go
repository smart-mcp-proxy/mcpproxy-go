//go:build !server

package config

// isServerEditionBuild is always false on the personal binary. See
// build_edition_server.go.
const isServerEditionBuild = false
