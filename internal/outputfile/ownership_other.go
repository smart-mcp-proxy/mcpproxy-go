//go:build !unix

package outputfile

// checkRootOwnership is a no-op on non-Unix platforms: Windows has no
// world-writable shared directory equivalent to /tmp in the default
// per-user setup, and its ACL model is not expressible as uid ownership.
func checkRootOwnership(configuredRoot, resolvedRoot string) error {
	return nil
}
