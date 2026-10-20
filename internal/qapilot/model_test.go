package qapilot

import (
	"testing"
)

// The model is the oracle for the conformance tests, so it gets its own
// hand-computed checks: each case below was written from the cited policy
// text, not by running the model.

func TestModel_QuarantinedManualNewToolPending(t *testing.T) {
	s := NewServer("srv", true, true, TrustManual, true)
	s.Discover(map[string]Contract{"t1": Benign})
	if got := s.Tools["t1"].Status; got != StatusPending {
		t.Fatalf("new tool on quarantined manual server: want pending, got %q", got)
	}
	if s.HasBaseline() {
		t.Fatal("a pending-only server has no baseline")
	}
	if got := s.Dispatch("t1", false); got != DispatchServerQuarantined {
		t.Fatalf("dispatch: want server_quarantined, got %q", got)
	}
	s.Discover(map[string]Contract{"t1": Benign})
	if got := s.Tools["t1"].Status; got != StatusPending {
		t.Fatalf("reconnect must keep pending, got %q", got)
	}
}

func TestModel_ReleasePromotesPendingNotChanged(t *testing.T) {
	s := NewServer("srv", true, true, TrustManual, true)
	s.Discover(map[string]Contract{"t1": Benign})
	s.SetQuarantined(false)
	if got := s.Tools["t1"].Status; got != StatusApproved {
		t.Fatalf("release: want approved, got %q", got)
	}
	s.Discover(map[string]Contract{"t1": Malicious})
	if got := s.Tools["t1"].Status; got != StatusChanged {
		t.Fatalf("rug pull: want changed, got %q", got)
	}
	s.SetQuarantined(true)
	s.SetQuarantined(false)
	if got := s.Tools["t1"].Status; got != StatusChanged {
		t.Fatalf("release must not clear changed, got %q", got)
	}
	if got := s.Dispatch("t1", false); got != DispatchChanged {
		t.Fatalf("dispatch: want changed, got %q", got)
	}
}

func TestModel_TrustedBaselineThenPendingAdditions(t *testing.T) {
	s := NewServer("srv", false, true, TrustManual, true)
	s.Discover(map[string]Contract{"t1": Benign, "t2": Benign})
	for _, n := range []string{"t1", "t2"} {
		if got := s.Tools[n].Status; got != StatusApproved {
			t.Fatalf("baseline: %s want approved, got %q", n, got)
		}
	}
	if !s.BaselineDecided {
		t.Fatal("baseline decision must be recorded")
	}
	s.Discover(map[string]Contract{"t1": Benign, "t2": Benign, "t3": Benign})
	if got := s.Tools["t3"].Status; got != StatusPending {
		t.Fatalf("post-baseline addition: want pending, got %q", got)
	}
	if got := s.Dispatch("t3", false); got != DispatchPendingApproval {
		t.Fatalf("dispatch t3: want pending_approval, got %q", got)
	}
	if got := s.Dispatch("t1", false); got != DispatchReady {
		t.Fatalf("dispatch t1: want ready, got %q", got)
	}
}

func TestModel_ScanModeGatesAdditionsHoldsChanges(t *testing.T) {
	s := NewServer("srv", false, true, TrustScan, true)
	s.Discover(map[string]Contract{"t1": Benign})
	s.Discover(map[string]Contract{"t1": Benign, "t2": Benign, "t3": Malicious})
	if got := s.Tools["t2"].Status; got != StatusApproved {
		t.Fatalf("clean addition in scan mode: want approved, got %q", got)
	}
	if got := s.Tools["t3"].Status; got != StatusPending {
		t.Fatalf("dangerous addition in scan mode: want pending, got %q", got)
	}
	s.Discover(map[string]Contract{"t1": BenignV2, "t2": Benign, "t3": Malicious})
	if got := s.Tools["t1"].Status; got != StatusChanged {
		t.Fatalf("benign change in scan mode: want changed, got %q", got)
	}
}

func TestModel_RevertAndOperatorApproval(t *testing.T) {
	s := NewServer("srv", false, true, TrustManual, true)
	s.Discover(map[string]Contract{"t1": Benign})
	s.Discover(map[string]Contract{"t1": Malicious})
	s.Discover(map[string]Contract{"t1": Benign})
	if got := s.Tools["t1"].Status; got != StatusApproved {
		t.Fatalf("revert: want approved, got %q", got)
	}
	s.Discover(map[string]Contract{"t1": Malicious})
	s.ApproveTools("t1")
	if got := s.Tools["t1"].Approved.Key; got != Malicious.Key {
		t.Fatalf("operator approval re-baselines on the current definition, got %q", got)
	}
	s.Discover(map[string]Contract{"t1": Benign})
	if got := s.Tools["t1"].Status; got != StatusChanged {
		t.Fatalf("returning to the OLD definition after approval is a change, got %q", got)
	}
}

func TestModel_AutoAndGlobalOffNeverHold(t *testing.T) {
	for _, s := range []*Server{
		NewServer("auto", false, true, TrustAuto, true),
		NewServer("off", false, true, TrustManual, false),
	} {
		s.Discover(map[string]Contract{"t1": Benign})
		s.Discover(map[string]Contract{"t1": Malicious, "t2": Malicious})
		for _, n := range []string{"t1", "t2"} {
			if got := s.Tools[n].Status; got != StatusApproved {
				t.Fatalf("%s: %s want approved, got %q", s.Name, n, got)
			}
			if got := s.Dispatch(n, false); got != DispatchReady {
				t.Fatalf("%s: dispatch %s want ready, got %q", s.Name, n, got)
			}
		}
	}
}

func TestModel_BlockApprovesAndDisables(t *testing.T) {
	s := NewServer("srv", false, true, TrustManual, true)
	s.Discover(map[string]Contract{"t1": Benign})
	s.Discover(map[string]Contract{"t1": Benign, "t2": Benign})
	s.SetToolDisabled("t2", true)
	if tl := s.Tools["t2"]; tl.Status != StatusApproved || !tl.Disabled {
		t.Fatalf("block of a pending tool: want approved+disabled, got %q disabled=%v", tl.Status, tl.Disabled)
	}
	if got := s.Dispatch("t2", false); got != DispatchBlockedByUser {
		t.Fatalf("dispatch blocked tool: want blocked_by_user, got %q", got)
	}
	s.SetToolDisabled("t2", false)
	if got := s.Dispatch("t2", false); got != DispatchReady {
		t.Fatalf("unblocked: want ready, got %q", got)
	}
}

func TestPolicy_Tables(t *testing.T) {
	if !AdmissionQuarantined(true, TrustManual) || !AdmissionQuarantined(true, TrustScan) || !AdmissionQuarantined(true, "") || !AdmissionQuarantined(true, "bogus") {
		t.Fatal("manual/scan/empty/unknown trust modes are quarantined on add")
	}
	if AdmissionQuarantined(true, TrustAuto) || AdmissionQuarantined(false, TrustManual) {
		t.Fatal("auto mode and global off are not quarantined on add")
	}
	if Authorize(CallerAgent, "unquarantine") || Authorize(CallerAgent, "approve_tools") || !Authorize(CallerAdmin, "unquarantine") || !Authorize(CallerAgent, "list") {
		t.Fatal("authorization table")
	}
	if TransitionAllowed(StatusChanged, StatusApproved, "scan_approved") {
		t.Fatal("a clean scan must never clear a changed record")
	}
	if !TransitionAllowed(StatusChanged, StatusApproved, "user_approve") || !TransitionAllowed(StatusPending, StatusApproved, "scan_approved") {
		t.Fatal("permitted reasons")
	}
	if !TransitionAllowed(StatusApproved, StatusChanged, "unknown") {
		t.Fatal("transitions into changed are always allowed")
	}
	cells := ClassifyCells()
	if len(cells) != 896 {
		t.Fatalf("decision table size: want 896, got %d", len(cells))
	}
	seen := map[string]bool{}
	for _, c := range cells {
		if seen[c.ID] {
			t.Fatalf("duplicate cell id %s", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestScenarios_WellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, sc := range Scenarios {
		if seen[sc.ID] {
			t.Fatalf("duplicate scenario id %s", sc.ID)
		}
		seen[sc.ID] = true
		if len(sc.Steps) == 0 || len(sc.Requirements) == 0 {
			t.Fatalf("%s: needs steps and requirements", sc.ID)
		}
		for _, r := range sc.Requirements {
			if _, ok := RequirementByID(r); !ok {
				t.Fatalf("%s: unknown requirement %s", sc.ID, r)
			}
		}
		// Every scenario must replay on the model without panicking and must
		// keep the inventory monotone (no tool disappears).
		s := NewServer("x", sc.Setup.Quarantined, sc.Setup.Enabled, sc.Setup.Trust, sc.Setup.GlobalQuarantine)
		known := map[string]bool{}
		for _, a := range sc.Steps {
			if a.Kind == ActDiscover {
				for n := range known {
					if _, ok := a.Inventory[n]; !ok {
						t.Fatalf("%s: inventory drops %s (pilot inventories are monotone)", sc.ID, n)
					}
				}
				for n := range a.Inventory {
					known[n] = true
				}
			}
			s.Apply(a)
		}
	}
	for _, c := range FixtureContracts() {
		if ContractText[c.Key] == "" {
			t.Fatalf("fixture %s has no text", c.Key)
		}
	}
}
