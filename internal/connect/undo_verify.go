package connect

import (
	"fmt"
	"strings"
)

// verifyFailure is the error of a connect whose post-write re-read failed. The
// write itself succeeded, so the text carries what the caller would otherwise
// lose: any note about the undo records and, for a browser-login connect, the
// backup path. An API-key connect with nothing to add keeps the text it always
// had.
func verifyFailure(err error, oauth bool, backupPath, recordNote string) error {
	var extra []string
	if recordNote != "" {
		extra = append(extra, "undo records: "+recordNote)
	}
	if oauth && backupPath != "" {
		extra = append(extra, "backup: "+backupPath)
	}
	if len(extra) == 0 {
		return fmt.Errorf("verification failed: %w", err)
	}
	return fmt.Errorf("verification failed: %w (the config was written; %s)", err, strings.Join(extra, "; "))
}
