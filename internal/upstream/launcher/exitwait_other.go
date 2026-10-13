//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package launcher

// waitExited has no non-reaping exit wait on this platform. Windows does
// not need one: there is no process-group ID to reuse, and the retirement
// kill there closes the Job Object instead.
func waitExited(int) bool { return false }
