package connect

import (
	"errors"
	"testing"
)

// An ambiguous outcome (the write landed, the verification re-read failed)
// neither commits nor aborts: it releases the in-flight claim so the
// reconciler can resolve the staged rotation from the file (FR-021a).
func TestSettle_AmbiguousWriteReleasesClaim(t *testing.T) {
	svc, _ := testServiceWithKey(t)
	minter := withFakeMinter(svc)
	issued, err := minter.Issue("cursor", CredentialIntent{})
	if err != nil {
		t.Fatal(err)
	}
	h := &credentialHandle{svc: svc, clientID: "cursor", mint: true, issued: issued, wrote: true}
	if err := h.settle(nil, errors.New("verification failed: boom")); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if minter.releases != 1 || minter.aborts != 0 || minter.commits != 0 {
		t.Fatalf("releases=%d aborts=%d commits=%d, want 1/0/0", minter.releases, minter.aborts, minter.commits)
	}
}

// A reconnect with no profile keeps the client's recorded binding, so the
// preview must show that binding, not the defaults (the write keeps it).
func TestPreview_ReconnectShowsRecordedBinding(t *testing.T) {
	svc, home := testServiceWithKey(t)
	svc.WithRequireMCPAuth(true)
	minter := withFakeMinter(svc)
	minter.bindings = map[string][2]string{"cursor": {"ro", "locked"}}
	seedClientConfig(t, home, "cursor")

	p, err := svc.PreviewWithIntent("cursor", "mcpproxy", CredentialIntent{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Profile != "ro" || p.Mode != "locked" {
		t.Fatalf("preview binding = %q/%q, want the recorded ro/locked", p.Profile, p.Mode)
	}
	empty := ""
	p, err = svc.PreviewWithIntent("cursor", "mcpproxy", CredentialIntent{Profile: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if p.Profile != "" || p.Mode != "switchable" {
		t.Fatalf("an explicit empty profile is All servers; got %q/%q", p.Profile, p.Mode)
	}
}
