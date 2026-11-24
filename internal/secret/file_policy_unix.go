//go:build !windows

package secret

func rejectPlatformAbsolutePath(path string) error {
	return nil
}
