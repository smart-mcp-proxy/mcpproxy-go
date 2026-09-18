package config

// MsgRequireMCPAuthOverridden is the boot notice and doctor finding text of
// contracts/config-keys.md for an explicit require_mcp_auth: false under an
// enabled server-edition block (Spec 107 FR-029).
const MsgRequireMCPAuthOverridden = "require_mcp_auth: false is overridden to true because server_edition.enabled is true"

// DoctorFindings returns the configuration findings `mcpproxy doctor` renders
// as runtime_warnings (Spec 107 T052). It reads the config it is handed — the
// caller passes the LIVE snapshot — and never mutates it. Edition-neutral
// findings live here; the server-only ones come from the build-tagged
// serverEditionDoctorFindings (a no-op on the personal build).
func DoctorFindings(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	return serverEditionDoctorFindings(cfg)
}
