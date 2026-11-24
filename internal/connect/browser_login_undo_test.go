//go:build !server

package connect

import (
	"path/filepath"
	"testing"
)

// Undo of an oauth connect restores the prior config and revokes nothing: the
// connect minted nothing.
func TestUndo_OfAnOAuthConnectRestoresAndRevokesNothing(t *testing.T) {
	svc, home := testServiceWithKey(t)
	oauthOn(svc)
	minter := withFakeMinter(svc)
	seedClientConfig(t, home, "claude-code")
	before := readConfig(t, home, "claude-code")

	res, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth})
	if err != nil || !res.Success {
		t.Fatalf("connect: %v %+v", err, res)
	}
	undone, err := svc.Undo("claude-code", "", filepath.Base(res.BackupPath))
	if err != nil || !undone.Success || undone.Action != "restored" {
		t.Fatalf("undo: %v %+v", err, undone)
	}
	if got := readConfig(t, home, "claude-code"); got != before {
		t.Fatalf("config not restored:\n%s", got)
	}
	if len(minter.forgetArgs) != 0 || undone.CredentialRevoked != "" {
		t.Fatalf("undo of an oauth connect must revoke nothing: %v %q", minter.forgetArgs, undone.CredentialRevoked)
	}
}
