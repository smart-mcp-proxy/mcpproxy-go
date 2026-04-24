package diagnostics

import "context"

// Built-in fixers registered at package init. These are intentionally
// minimal/safe implementations that can operate with no runtime dependency.
// More advanced fixers (e.g. oauth_reauth which needs the OAuth coordinator)
// are registered by higher layers at startup.

func init() {
	// stdio_show_last_logs is a non-destructive read-only probe. It returns a
	// stock preview string; a higher-layer implementation may override this
	// with an actual log tail via Register().
	Register("stdio_show_last_logs", func(_ context.Context, req FixRequest) (FixResult, error) {
		return FixResult{
			Outcome: OutcomeSuccess,
			Preview: "Server '" + req.ServerID + "' log tail unavailable in this build — enable server-side log access to view the last 50 lines here.",
		}, nil
	})

	// config_migrate_deprecated is a destructive placeholder that must be
	// overridden by a higher layer that owns the config file. It returns a
	// blocked outcome by default so nothing changes unless explicitly wired.
	Register("config_migrate_deprecated", func(_ context.Context, req FixRequest) (FixResult, error) {
		if req.Mode == ModeDryRun {
			return FixResult{
				Outcome: OutcomeSuccess,
				Preview: "Would rewrite deprecated fields in mcp_config.json for server '" + req.ServerID + "' (no changes made).",
			}, nil
		}
		return FixResult{
			Outcome:    OutcomeBlocked,
			FailureMsg: "config migration fixer has not been wired to the live config service in this build",
		}, nil
	})

	// server_disable_scanner is a destructive placeholder — higher layer should
	// override it to mutate the server's skip_scanner flag.
	Register("server_disable_scanner", func(_ context.Context, req FixRequest) (FixResult, error) {
		if req.Mode == ModeDryRun {
			return FixResult{
				Outcome: OutcomeSuccess,
				Preview: "Would set skip_scanner=true on server '" + req.ServerID + "' (no changes made).",
			}, nil
		}
		return FixResult{
			Outcome:    OutcomeBlocked,
			FailureMsg: "disable-scanner fixer has not been wired to the live config service in this build",
		}, nil
	})

	// oauth_reauth is a destructive placeholder — higher layer should override
	// with a call to oauth.coordinator.InitiateLogin.
	Register("oauth_reauth", func(_ context.Context, req FixRequest) (FixResult, error) {
		if req.Mode == ModeDryRun {
			return FixResult{
				Outcome: OutcomeSuccess,
				Preview: "Would launch OAuth login flow for server '" + req.ServerID + "' (no changes made).",
			}, nil
		}
		return FixResult{
			Outcome:    OutcomeBlocked,
			FailureMsg: "oauth re-auth fixer has not been wired to the OAuth coordinator in this build",
		}, nil
	})
}
