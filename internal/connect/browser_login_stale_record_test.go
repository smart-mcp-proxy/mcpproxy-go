//go:build !server

package connect

import (
	"os"
	"strings"
	"testing"
)

// A created-file record is stale once the file it describes is gone. A later
// connect that creates the file again must not leave the old record usable.
func TestOAuthConnect_StaleCreatedFileRecordCannotAuthoriseAnUndo(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	path := ConfigPath("claude-code", home)

	// A creates content X (same entry name as B below)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	x := readConfig(t, home, "claude-code")
	_ = os.Remove(path)

	// B creates content Y: a different address, so a different file
	svc.listenAddr = "127.0.0.1:9999"
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	if got := undoFiles(t, home); len(got) != 1 {
		t.Fatalf("only the current created-file record may exist, got %v", got)
	}
	bRecord := undoFiles(t, home)[0]

	// the user edits Y back to X
	if err := os.WriteFile(path, []byte(x), 0o600); err != nil {
		t.Fatal(err)
	}
	undone, err := svc.Undo("claude-code", "", "")
	if err != nil || undone.Action != "conflict" {
		t.Fatalf("want conflict, got %v %+v", err, undone)
	}
	if after, _ := os.ReadFile(path); string(after) != x {
		t.Fatal("the changed file must be left alone")
	}
	if _, err := os.Stat(bRecord); err != nil {
		t.Fatalf("B's record must still be there: %v", err)
	}
}

// A successful Undo leaves no created-file record of that entry behind, even a
// leftover of a race.
func TestUndo_RemovesEveryCreatedFileRecordOfTheEntry(t *testing.T) {
	svc, home := testService(t)
	oauthOn(svc)
	withFakeMinter(svc)
	if _, err := svc.ConnectWithOptions("claude-code", "", ConnectOptions{Auth: AuthOAuth}); err != nil {
		t.Fatal(err)
	}
	// a racer's record for the same entry, made by hand
	rec := undoFiles(t, home)[0]
	raw, _ := os.ReadFile(rec)
	other := strings.Replace(rec, ".undo.new.", ".undo.new.racer", 1)
	if err := os.WriteFile(other, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if undone, err := svc.Undo("claude-code", "", ""); err != nil || undone.Action != "deleted" {
		t.Fatalf("%v %+v", err, undone)
	}
	if got := undoFiles(t, home); len(got) != 0 {
		t.Fatalf("stray records %v", got)
	}
}
