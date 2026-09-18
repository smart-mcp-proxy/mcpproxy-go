package config

import (
	"fmt"
	"io"

	"go.uber.org/zap"
)

// LoadDiagnostic is one non-fatal finding recorded while a configuration file
// was loaded: a key the server edition no longer supports and dropped, or a
// deprecated key it still decodes (Spec 107 FR-032/FR-033/FR-035).
//
// The loader has no logger (loadConfigFile returns only an error and main.go
// builds the logger after the load), and once the typed decode has dropped the
// keys they cannot be reconstructed later, so the loader records them on the
// Config and LogLoadDiagnostics emits them once a logger exists. The personal
// build carries the server-edition blocks as opaque JSON and records nothing.
type LoadDiagnostic struct {
	// Key is the JSON path of the offending key (e.g.
	// "server_edition.max_user_servers", "mcpServers[0].auth_broker.mode").
	Key string
	// Message is the exact operator-facing text from contracts/config-keys.md.
	Message string
}

// LoadDiagnostics returns the diagnostics recorded by the load that produced
// this Config, in file order. A Config built in code, or one decoded with a
// bare json.Unmarshal, has none.
func (c *Config) LoadDiagnostics() []LoadDiagnostic {
	if c == nil {
		return nil
	}
	return c.loadDiagnostics
}

// LogLoadDiagnostics emits one WARN line per recorded diagnostic. It is called
// once after the logger exists (cmd/mcpproxy/main.go right after
// logs.SetupLogger) and after each successful hot reload — never by the
// loader, which has no logger. A nil config or logger, or no diagnostics, is a
// silent no-op.
func LogLoadDiagnostics(cfg *Config, logger *zap.Logger) {
	if cfg == nil || logger == nil {
		return
	}
	for _, d := range cfg.loadDiagnostics {
		logger.Warn(d.Message, zap.String("key", d.Key))
	}
}

// WriteLoadDiagnostics prints one `warning: <message> (key: <path>)` line per
// recorded diagnostic to w. It is the logger-less twin of LogLoadDiagnostics
// for the CLI subcommands, which have no zap logger but do load the file and
// — for `upstream add|remove` in standalone mode, `telemetry enable|disable`
// and the API-key reset — write it back, so a removed key would otherwise be
// erased with no operator-visible warning (FR-032). Callers pass stderr so
// `-o json` consumers of stdout are never disturbed. A nil config, a nil
// writer or no diagnostics writes nothing.
func WriteLoadDiagnostics(cfg *Config, w io.Writer) {
	if cfg == nil || w == nil {
		return
	}
	for _, d := range cfg.loadDiagnostics {
		fmt.Fprintf(w, "warning: %s (key: %s)\n", d.Message, d.Key)
	}
}
