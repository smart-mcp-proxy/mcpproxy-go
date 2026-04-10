package telemetry

import "testing"

func TestValidErrorCategoriesEnum(t *testing.T) {
	want := []ErrorCategory{
		ErrCatOAuthRefreshFailed,
		ErrCatOAuthTokenExpired,
		ErrCatUpstreamConnectTimeout,
		ErrCatUpstreamConnectRefused,
		ErrCatUpstreamHandshakeFailed,
		ErrCatToolQuarantineBlocked,
		ErrCatDockerPullFailed,
		ErrCatDockerRunFailed,
		ErrCatIndexRebuildFailed,
		ErrCatConfigReloadFailed,
		ErrCatSocketBindFailed,
	}

	if got := len(validErrorCategories); got != len(want) {
		t.Errorf("validErrorCategories size = %d, want %d", got, len(want))
	}

	for _, c := range want {
		if !IsValidErrorCategory(c) {
			t.Errorf("expected %q to be a valid error category", c)
		}
	}

	if IsValidErrorCategory(ErrorCategory("nonexistent_category")) {
		t.Error("expected unknown category to be rejected")
	}
}
