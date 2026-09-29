package auth

import "testing"

func TestScopedView_ConfinedAnonymousBecomesNonAdminForManagement(t *testing.T) {
	admin := AnonymousContext()
	view := ScopedView(admin, true)
	if view == admin || view.IsAdmin() || !view.Anonymous {
		t.Fatalf("confined anonymous view must be a non-admin copy: %#v", view)
	}
	if !view.CanAccessServer("any-server") || !view.HasPermission(PermRead) || !view.HasPermission(PermDestructive) {
		t.Fatalf("the view must defer capability limits to the effective profile: %#v", view)
	}
	if got := ScopedView(admin, false); got != admin {
		t.Fatal("unconfined anonymous callers must keep the historical context")
	}
	if got := ScopedView(nil, false); got != nil {
		t.Fatal("unconfined in-process calls keep the nil context")
	}
	if got := ScopedView(nil, true); got == nil || got.IsAdmin() {
		t.Fatal("a nil confined anonymous caller must still receive a non-admin view")
	}
}
