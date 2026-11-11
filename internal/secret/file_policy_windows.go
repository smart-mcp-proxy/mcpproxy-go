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
		afterVol := strings.TrimPrefix(path, vol)
		if afterVol == "" {
			return fmt.Errorf("secret file path %q must be absolute", path)
		}
		if afterVol[0] != '\\' && afterVol[0] != '/' {
			// Drive-relative path such as C:foo (no root after the colon).
			return fmt.Errorf("secret file path %q must be absolute", path)
		}
	}
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		return fmt.Errorf("secret file path %q must be absolute", path)
	}
	return nil
}
