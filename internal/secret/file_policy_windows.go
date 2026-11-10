//go:build windows

package secret

import (
	"fmt"
	"path/filepath"
	"strings"
)

func rejectPlatformAbsolutePath(path string) error {
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return fmt.Errorf("secret file path %q uses a forbidden prefix", path)
	}
	vol := filepath.VolumeName(path)
	if vol != "" && len(vol) == 2 && vol[1] == ':' {
		rest := strings.TrimPrefix(path, vol)
		rest = strings.TrimPrefix(rest, `\`)
		rest = strings.TrimPrefix(rest, `/`)
		if rest != "" && !strings.HasPrefix(rest, `\`) && !strings.HasPrefix(rest, `/`) {
			// Drive-relative path such as C:foo
			return fmt.Errorf("secret file path %q must be absolute", path)
		}
	}
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		return fmt.Errorf("secret file path %q must be absolute", path)
	}
	return nil
}
