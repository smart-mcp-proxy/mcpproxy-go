//go:build !server

package connect

import (
	"os"
	"testing"
)

// A created-file record of another entry says nothing about this file: it must
// neither block the legitimate undo of a key connect nor authorise one.
func TestUndo_ForeignCreatedFileRecordIsIgnored(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)

	if _, err := svc.ConnectWithOptions("claude-code", "one", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if res, err := svc.ConnectWithOptions("claude-code", "two", ConnectOptions{}); err != nil || !res.Success {
		t.Fatalf("%v %+v", err, res)
	}
	if got := undoFiles(t, home); len(got) != 1 {
		t.Fatalf("the other entry's record is not ours to retire, got %v", got)
	}
	if undone, err := svc.Undo("claude-code", "two", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("untouched key file with a foreign record beside it: %v %+v", err, undone)
	}
	if got := undoFiles(t, home); len(got) != 1 {
		t.Fatalf("the foreign record must survive, got %v", got)
	}
}
