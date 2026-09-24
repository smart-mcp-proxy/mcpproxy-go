package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"go.uber.org/zap"
)

// generatedAPIKeyInfo describes a freshly auto-generated admin API key and what
// happened when we tried to persist it to the config file.
type generatedAPIKeyInfo struct {
	APIKey     string
	Listen     string
	Source     string
	ConfigPath string
	SaveErr    error
}

// webUIURL is the ready-to-use Web UI URL, credential included. It is only ever
// written to an interactive terminal.
func (i generatedAPIKeyInfo) webUIURL() string {
	return i.webUIURLWith(i.APIKey)
}

// webUIURLLogSafe is the same URL built with the key ALREADY masked.
//
// codex round 3 finding 1: building it with the raw key and handing the result
// to a redactor is one parse failure away from publishing the key - `listen` is
// operator-supplied, and a value url.Parse rejects sends the redactor down its
// regex fallback, which has no `apikey` rule. The raw key is never put into a
// string bound for the log sink in the first place.
func (i generatedAPIKeyInfo) webUIURLLogSafe() string {
	return i.webUIURLWith(maskAPIKey(i.APIKey))
}

func (i generatedAPIKeyInfo) webUIURLWith(key string) string {
	return fmt.Sprintf("http://%s/ui/?apikey=%s", i.Listen, key)
}

// stderrIsTerminal reports whether the human sink is an interactive terminal.
var stderrIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// announceGeneratedAPIKey reports a newly auto-generated admin API key.
//
// SEC-01: the raw key used to be written to the PERSISTENT log sink three
// times - as `api_key`, embedded in the `web_ui_url` field, and once more on
// the config-save-failure path - so ~/Library/Logs/mcpproxy/main.log held the
// root credential in plaintext, with the log file's own permissions and its own
// retention.
//
// The split: the raw key goes only to `out` (stderr), and only when stderr is
// an interactive terminal. The operator has to be able to see it once, but a
// service manager or a CI redirect persists that stream just like a log file,
// so withholding it there is the whole point rather than an inconvenience
// (codex round 3 finding 2). The log sink gets the masked prefix, a Web-UI URL
// built with the key already masked, and the config path - which is where the
// key is authoritatively stored.
//
// Never stdout: an empty or ":0" listen address selects the stdio MCP
// transport, where stdout carries JSON-RPC frames.
func announceGeneratedAPIKey(logger *zap.Logger, out io.Writer, isTTY bool, info generatedAPIKeyInfo) {
	logger.Warn("API key was auto-generated for security",
		zap.String("api_key_prefix", maskAPIKey(info.APIKey)),
		zap.String("web_ui_url", info.webUIURLLogSafe()),
		zap.String("config_path", info.ConfigPath),
		zap.String("source", info.Source))

	if info.SaveErr != nil {
		logger.Warn("Failed to save the auto-generated API key to the config file; "+
			"it cannot be recovered and a different key will be generated on the next restart. "+
			"Set MCPPROXY_API_KEY, or make the config path writable, and restart",
			zap.Error(info.SaveErr),
			zap.String("config_path", info.ConfigPath))
	} else {
		logger.Info("Auto-generated API key saved to config file",
			zap.String("config_path", info.ConfigPath))
	}

	writeGeneratedAPIKeyBanner(out, isTTY, info)
}

// writeGeneratedAPIKeyBanner writes the human-facing banner. On a terminal it
// carries the key itself; otherwise it only says where the key lives, or - when
// it could not be stored anywhere - how to supply one that can.
func writeGeneratedAPIKeyBanner(out io.Writer, isTTY bool, info generatedAPIKeyInfo) {
	if out == nil {
		return
	}
	frame := strings.Repeat("*", 80)
	var b strings.Builder

	b.WriteString(frame + "\n")
	b.WriteString("An API key was auto-generated for this instance.\n")

	if isTTY {
		b.WriteString("API key:    " + info.APIKey + "\n")
		b.WriteString("Web UI:     " + info.webUIURL() + "\n")
	} else {
		b.WriteString("The key is not printed here because this output is not a terminal,\n")
		b.WriteString("and a redirected stream is as persistent as a log file.\n")
	}

	if info.SaveErr != nil {
		b.WriteString("WARNING: it could NOT be saved to " + info.ConfigPath + "\n")
		b.WriteString("         (" + info.SaveErr.Error() + ")\n")
		if !isTTY {
			b.WriteString("There is therefore no way to read this key back. Set MCPPROXY_API_KEY to a\n")
			b.WriteString("key of your own, or make that path writable, and restart.\n")
		} else {
			b.WriteString("Copy it now: a different key is generated on the next restart.\n")
		}
	} else {
		b.WriteString("Stored in:  " + info.ConfigPath + " (field \"api_key\")\n")
		b.WriteString("For your security it is no longer written to the log files.\n")
	}
	b.WriteString(frame + "\n")

	_, _ = io.WriteString(out, b.String())
}
