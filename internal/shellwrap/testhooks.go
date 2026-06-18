package shellwrap

// This file exposes a few narrowly-scoped test seams so that packages OTHER
// than shellwrap (notably internal/upstream/core) can build hermetic
// integration tests over the docker-path resolution + spawn chain — e.g. the
// GitHub #696 "Docker Desktop installed but docker is not on the spawn PATH"
// scenario, which can only be reproduced end-to-end by driving the REAL
// ResolveDockerPath (not a stub) under a controlled PATH and well-known-path
// list, then feeding its result through core.setupDockerIsolation.
//
// They are intentionally named *ForTest and do nothing a caller would want in
// production; keeping them in a non-_test.go file is the only way to make them
// reachable from a sibling package's test binary (Go's export_test.go trick is
// package-local). Do NOT call these outside tests.

// SetWellKnownDockerPathsForTest overrides the well-known docker install
// locations probed by ResolveDockerPath and returns a restore func that
// reinstalls the previous list. Pass a func returning the absolute path(s) of a
// fake docker binary to simulate a Docker Desktop bundle that is reachable only
// off the standard spawn PATH.
func SetWellKnownDockerPathsForTest(fn func() []string) (restore func()) {
	prev := wellKnownDockerPathsFn
	wellKnownDockerPathsFn = fn
	return func() { wellKnownDockerPathsFn = prev }
}

// ResetDockerPathCacheForTest clears the process-wide docker-path resolution
// cache (path, source, error, expiry) so a test starts from a clean slate
// regardless of what an earlier test or the host environment resolved.
func ResetDockerPathCacheForTest() {
	resetDockerPathCacheForTest()
}
