package runtime

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// buildIsolationMaps projects a server's isolation configuration into the two
// generic maps that GetAllServers emits: the `isolation` override block and the
// `isolation_effective` resolution block.
//
// `isolation.enabled` is the EFFECTIVE state — what actually happens at spawn
// time. The raw per-server override travels beside it as `enabled_override`
// (and `mode_override`), present only when the server actually set it, so
// "inherits the global setting" stays distinguishable from an explicit opt-out
// instead of being flattened to false (GH #1142).
//
// Returns (nil, nil) when the server has no local process to isolate and no
// stale override to display.
func buildIsolationMaps(
	globalIsolation *config.DockerIsolationConfig,
	sc *config.ServerConfig,
) (isolation, effective map[string]interface{}) {
	iso, eff := contracts.BuildIsolationView(globalIsolation, sc)
	if iso == nil {
		return nil, nil
	}

	isolation = map[string]interface{}{
		"enabled": iso.Enabled,
	}
	if iso.EnabledOverride != nil {
		isolation["enabled_override"] = *iso.EnabledOverride
	}
	if iso.ModeOverride != "" {
		isolation["mode_override"] = iso.ModeOverride
	}
	if iso.Image != "" {
		isolation["image"] = iso.Image
	}
	if iso.NetworkMode != "" {
		isolation["network_mode"] = iso.NetworkMode
	}
	if len(iso.ExtraArgs) > 0 {
		isolation["extra_args"] = iso.ExtraArgs
	}
	if iso.WorkingDir != "" {
		isolation["working_dir"] = iso.WorkingDir
	}

	if eff != nil {
		effective = map[string]interface{}{
			"mode":      eff.Mode,
			"isolated":  eff.Isolated,
			"inherited": eff.Inherited,
		}
		if eff.GlobalMode != "" {
			effective["global_mode"] = eff.GlobalMode
		}
		if eff.Source != "" {
			effective["source"] = eff.Source
		}
	}

	return isolation, effective
}
