package telemetry

import "testing"

func TestEnvOverridePrecedence(t *testing.T) {
	cases := []struct {
		name        string
		doNotTrack  string
		ci          string
		mcpproxyEnv string
		wantDis     bool
		wantReason  EnvDisabledReason
	}{
		{"no env vars", "", "", "", false, EnvDisabledNone},
		{"DO_NOT_TRACK=1", "1", "", "", true, EnvDisabledByDoNotTrack},
		{"DO_NOT_TRACK=true", "true", "", "", true, EnvDisabledByDoNotTrack},
		{"DO_NOT_TRACK=0 ignored", "0", "", "", false, EnvDisabledNone},
		{"DO_NOT_TRACK=yes", "yes", "", "", true, EnvDisabledByDoNotTrack},
		{"CI=true", "", "true", "", true, EnvDisabledByCI},
		{"CI=1", "", "1", "", true, EnvDisabledByCI},
		{"CI=false ignored", "", "false", "", false, EnvDisabledNone},
		{"MCPPROXY_TELEMETRY=false", "", "", "false", true, EnvDisabledByMCPProxy},
		{"MCPPROXY_TELEMETRY=False (case)", "", "", "False", true, EnvDisabledByMCPProxy},
		{"MCPPROXY_TELEMETRY=true", "", "", "true", false, EnvDisabledNone},
		{"DO_NOT_TRACK overrides CI", "1", "true", "false", true, EnvDisabledByDoNotTrack},
		{"CI overrides MCPPROXY_TELEMETRY", "", "true", "true", true, EnvDisabledByCI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DO_NOT_TRACK", c.doNotTrack)
			t.Setenv("CI", c.ci)
			t.Setenv("MCPPROXY_TELEMETRY", c.mcpproxyEnv)

			gotDis, gotReason := IsDisabledByEnv()
			if gotDis != c.wantDis {
				t.Errorf("disabled = %v, want %v", gotDis, c.wantDis)
			}
			if gotReason != c.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, c.wantReason)
			}
		})
	}
}
