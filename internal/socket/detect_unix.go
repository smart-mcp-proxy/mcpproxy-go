//go:build !windows

package socket

// isPipeAvailable is not used on Unix systems (returns false as fallback).
func isPipeAvailable(endpoint string) bool {
	return false
}
