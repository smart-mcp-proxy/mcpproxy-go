package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Review: a blank client or grant id must never reach the daemon, where a
// trailing slash could be routed to another handler.
func TestClientRevokeLogin_BlankIDsNeverSendARequest(t *testing.T) {
	rec := newRESTRecorder(t, map[string]cannedResponse{})
	for _, args := range [][]string{{"", "g"}, {"cursor", ""}, {"cursor", "  "}, {" ", "g"}} {
		_, _, err := runCLI(t, GetClientCommand, "table", append([]string{"revoke-login"}, args...)...)
		require.Error(t, err, "%q", args)
	}
	require.Empty(t, rec.requests)
}
