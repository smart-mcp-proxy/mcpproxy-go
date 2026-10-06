package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// The reconnect preview and the write share one binding rule, so they cannot
// disagree (R3): for every record state and intent, PreviewBinding equals what
// Issue then applies.
func TestConnectMinter_PreviewBindingEqualsIssue(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(h *svcHarness)
		intent   connect.CredentialIntent
		wantPin  string
		wantMode string
	}{
		{"active ro locked, nil intent keeps it", func(h *svcHarness) { h.mint("cursor", "ro", nil) },
			connect.CredentialIntent{}, "ro", "locked"},
		{"explicit empty profile is All servers", func(h *svcHarness) { h.mint("cursor", "ro", nil) },
			connect.CredentialIntent{Profile: strp("")}, "", "switchable"},
		{"explicit profile locks", func(h *svcHarness) { h.mint("cursor", "ro", nil) },
			connect.CredentialIntent{Profile: strp("full")}, "full", "locked"},
		{"mode only keeps the pin", func(h *svcHarness) { h.mint("cursor", "ro", nil) },
			connect.CredentialIntent{Mode: strp("switchable")}, "ro", "switchable"},
		{"revoked with a preserved binding re-mints with it", func(h *svcHarness) {
			h.mint("cursor", "ro", nil)
			_, err := h.sm.ForgetClientCredential("cursor")
			require.NoError(h.t, err)
		}, connect.CredentialIntent{}, "ro", "locked"},
		{"revoked with an explicit profile still wins", func(h *svcHarness) {
			h.mint("cursor", "ro", nil)
			_, err := h.sm.ForgetClientCredential("cursor")
			require.NoError(h.t, err)
		}, connect.CredentialIntent{Profile: strp("full")}, "full", "locked"},
		{"revoked with no binding starts from the defaults", func(h *svcHarness) {
			h.mint("cursor", "", nil)
			_, err := h.sm.ForgetClientCredential("cursor")
			require.NoError(h.t, err)
		}, connect.CredentialIntent{}, "", "switchable"},
		{"no record starts from the defaults", func(h *svcHarness) {}, connect.CredentialIntent{}, "", "switchable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSvcHarness(t)
			tc.setup(h)
			m := h.svc.ConnectMinter()
			pin, mode, err := m.PreviewBinding("cursor", tc.intent)
			require.NoError(t, err)
			require.Equal(t, tc.wantPin, pin)
			require.Equal(t, tc.wantMode, mode)

			tc.intent.ActorKind, tc.intent.Surface = "api_key", "api"
			issued, err := m.Issue("cursor", tc.intent)
			require.NoError(t, err)
			require.Equal(t, pin, issued.Profile, "the write applies what the preview showed")
			require.Equal(t, mode, issued.Mode)
			require.NoError(t, m.Abort("cursor", tc.intent, issued))
		})
	}

	t.Run("expired record keeps its binding", func(t *testing.T) {
		h := newSvcHarness(t)
		h.clock = time.Now().Add(-auth.MaxTokenExpiry + 300*time.Millisecond)
		h.mint("cursor", "ro", nil)
		time.Sleep(450 * time.Millisecond)
		h.clock = time.Now()
		pin, mode, err := h.svc.ConnectMinter().PreviewBinding("cursor", connect.CredentialIntent{})
		require.NoError(t, err)
		require.Equal(t, "ro", pin)
		require.Equal(t, "locked", mode)
	})

	t.Run("unknown profile is refused like the write", func(t *testing.T) {
		h := newSvcHarness(t)
		_, _, err := h.svc.ConnectMinter().PreviewBinding("cursor", connect.CredentialIntent{Profile: strp("nope")})
		var val *ValidationError
		require.ErrorAs(t, err, &val)
		require.Equal(t, "profile", val.Field)
	})
}
