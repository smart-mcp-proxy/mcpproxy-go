//go:build !windows

package redirectionguard

// enforced is always false off Windows: RedirectionGuard is a Windows mitigation.
func enforced() bool { return false }
