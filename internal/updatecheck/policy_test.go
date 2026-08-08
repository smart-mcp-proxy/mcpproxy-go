package updatecheck

import (
	"testing"

	"go.uber.org/zap"
)

// Spec 092 FR-015: the policy must be computed live so a config hot-reload or
// an environment override is visible on the very next read.
func TestCheckerPolicyReflectsConfigAndEnvironment(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv(EnvDisableAutoUpdate, "")
	t.Setenv(EnvAllowPrereleaseUpdates, "")

	c := New(zap.NewNop(), "v1.0.0")

	p := c.Policy()
	if !p.Enabled {
		t.Fatalf("default policy should be enabled, got %+v", p)
	}
	if p.Channel != PolicyChannelStable {
		t.Fatalf("default channel = %q, want %q", p.Channel, PolicyChannelStable)
	}
	if p.NudgesSuppressed {
		t.Fatalf("nudges should not be suppressed outside CI, got %+v", p)
	}

	// Hot reload onto the RC channel.
	c.SetConfig(true, true)
	if got := c.Policy().Channel; got != PolicyChannelRC {
		t.Fatalf("after SetConfig(rc) channel = %q, want %q", got, PolicyChannelRC)
	}

	// Config kill switch.
	c.SetConfig(false, true)
	if c.Policy().Enabled {
		t.Fatalf("update_check.enabled=false must disable the policy")
	}

	// Env kill switch wins over an enabled config.
	c.SetConfig(true, false)
	t.Setenv(EnvDisableAutoUpdate, "true")
	if c.Policy().Enabled {
		t.Fatalf("%s=true must win over update_check.enabled=true", EnvDisableAutoUpdate)
	}

	// Env prerelease override wins over channel=stable.
	t.Setenv(EnvDisableAutoUpdate, "")
	t.Setenv(EnvAllowPrereleaseUpdates, "true")
	if got := c.Policy().Channel; got != PolicyChannelRC {
		t.Fatalf("%s=true must force the rc channel, got %q", EnvAllowPrereleaseUpdates, got)
	}
}

// The CI rule is captured at construction (Spec 079 FR-019) and reported by
// the policy, so a tray can stay quiet without re-deriving the rule itself.
func TestCheckerPolicyReportsNudgeSuppressionInCI(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv(EnvDisableAutoUpdate, "")
	t.Setenv(EnvAllowPrereleaseUpdates, "")

	c := New(zap.NewNop(), "v1.0.0")
	p := c.Policy()
	if !p.NudgesSuppressed {
		t.Fatalf("CI=true must suppress nudges, got %+v", p)
	}
	if !p.Enabled {
		t.Fatalf("nudge suppression is not a kill switch; checks stay enabled: %+v", p)
	}
}

// Without a checker there is nothing that could perform an automatic check, so
// the reported policy says so rather than leaving the client to infer it.
func TestUnavailablePolicyIsExplicitlyDisabled(t *testing.T) {
	t.Setenv("CI", "")
	p := UnavailablePolicy()
	if p.Enabled {
		t.Fatalf("UnavailablePolicy must be disabled, got %+v", p)
	}
	if p.Channel != PolicyChannelStable {
		t.Fatalf("UnavailablePolicy channel = %q, want %q", p.Channel, PolicyChannelStable)
	}
	if p.NudgesSuppressed {
		t.Fatalf("outside CI nudges are not suppressed, got %+v", p)
	}

	t.Setenv("CI", "1")
	if !UnavailablePolicy().NudgesSuppressed {
		t.Fatalf("CI=1 must suppress nudges even without a checker")
	}
}
