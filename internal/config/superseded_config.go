package config

import (
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// MsgSupersededConfigNotRecorded is the warning when the identity of the
// config file about to be replaced cannot be read: a hardlink made to it
// earlier will not be refused by upstream ${file:} reads.
const MsgSupersededConfigNotRecorded = "could not record the identity of the config file being replaced; " +
	"a hardlink made to it earlier is not refused by upstream ${file:} reads"

// noteSupersededConfig records the config file's current identity before a
// rename replaces it (secret.NoteSupersededProxyFile) and warns, naming the
// file only, when that fails.
func noteSupersededConfig(path string) {
	if err := secret.NoteSupersededProxyFile(path); err != nil {
		emitLoadWarningVisible(MsgSupersededConfigNotRecorded, "config_file", filepath.Base(path))
	}
}
