package telemetry

import (
	"sync"
	"testing"
)

func TestNewCounterRegistry(t *testing.T) {
	r := NewCounterRegistry()
	snap := r.Snapshot()

	for _, surface := range []string{"mcp", "cli", "webui", "tray", "unknown"} {
		if snap.SurfaceCounts[surface] != 0 {
			t.Errorf("expected zero count for %s, got %d", surface, snap.SurfaceCounts[surface])
		}
	}
	if snap.UpstreamToolCallCountBucket != "0" {
		t.Errorf("expected upstream bucket = 0, got %q", snap.UpstreamToolCallCountBucket)
	}
	if len(snap.BuiltinToolCalls) != 0 {
		t.Errorf("expected empty builtin map, got %v", snap.BuiltinToolCalls)
	}
	if len(snap.RESTEndpointCalls) != 0 {
		t.Errorf("expected empty REST map, got %v", snap.RESTEndpointCalls)
	}
	if len(snap.ErrorCategoryCounts) != 0 {
		t.Errorf("expected empty error categories, got %v", snap.ErrorCategoryCounts)
	}
	if len(snap.DoctorChecks) != 0 {
		t.Errorf("expected empty doctor map, got %v", snap.DoctorChecks)
	}
}

func TestParseClientSurface(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   Surface
	}{
		{"empty", "", SurfaceUnknown},
		{"tray with version", "tray/v0.21.0", SurfaceTray},
		{"cli with version", "cli/v0.21.0", SurfaceCLI},
		{"webui with version", "webui/v0.21.0", SurfaceWebUI},
		{"mcp prefix", "mcp/v0.21.0", SurfaceMCP},
		{"no slash", "tray", SurfaceTray},
		{"unknown prefix", "spoof/v1.0", SurfaceUnknown},
		{"uppercase", "TRAY/v0.21.0", SurfaceTray},
		{"malformed", "/", SurfaceUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseClientSurface(c.header)
			if got != c.want {
				t.Errorf("ParseClientSurface(%q) = %v, want %v", c.header, got, c.want)
			}
		})
	}
}

func TestRecordSurfaceConcurrent(t *testing.T) {
	r := NewCounterRegistry()

	var wg sync.WaitGroup
	const goroutines = 100
	const incrementsPerGoroutine = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				r.RecordSurface(SurfaceCLI)
				r.RecordSurface(SurfaceMCP)
				r.RecordSurface(SurfaceTray)
			}
		}()
	}
	wg.Wait()

	snap := r.Snapshot()
	expected := int64(goroutines * incrementsPerGoroutine)
	for _, s := range []string{"cli", "mcp", "tray"} {
		if snap.SurfaceCounts[s] != expected {
			t.Errorf("surface %s = %d, want %d", s, snap.SurfaceCounts[s], expected)
		}
	}
	if snap.SurfaceCounts["webui"] != 0 {
		t.Errorf("webui should be 0, got %d", snap.SurfaceCounts["webui"])
	}
}

func TestUpstreamBucketBoundaries(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1-10"},
		{10, "1-10"},
		{11, "11-100"},
		{100, "11-100"},
		{101, "101-1000"},
		{1000, "101-1000"},
		{1001, "1000+"},
		{99999, "1000+"},
	}
	for _, c := range cases {
		got := bucketUpstream(c.n)
		if got != c.want {
			t.Errorf("bucketUpstream(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestRecordBuiltinToolKnownAndUnknown(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordBuiltinTool("retrieve_tools")
	r.RecordBuiltinTool("retrieve_tools")
	r.RecordBuiltinTool("code_execution")

	// Unknown names must be silently dropped (no panic, no entry).
	r.RecordBuiltinTool("github:create_issue")
	r.RecordBuiltinTool("my-canary-server:secret-tool")
	r.RecordBuiltinTool("")

	snap := r.Snapshot()
	if snap.BuiltinToolCalls["retrieve_tools"] != 2 {
		t.Errorf("retrieve_tools = %d, want 2", snap.BuiltinToolCalls["retrieve_tools"])
	}
	if snap.BuiltinToolCalls["code_execution"] != 1 {
		t.Errorf("code_execution = %d, want 1", snap.BuiltinToolCalls["code_execution"])
	}
	if _, ok := snap.BuiltinToolCalls["github:create_issue"]; ok {
		t.Error("upstream tool name leaked into builtin map")
	}
	if _, ok := snap.BuiltinToolCalls["my-canary-server:secret-tool"]; ok {
		t.Error("canary upstream name leaked into builtin map")
	}
}

func TestRecordRESTRequestNestedMap(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordRESTRequest("GET", "/api/v1/servers", "2xx")
	r.RecordRESTRequest("GET", "/api/v1/servers", "2xx")
	r.RecordRESTRequest("GET", "/api/v1/servers", "5xx")
	r.RecordRESTRequest("POST", "/api/v1/servers/{name}/enable", "2xx")
	r.RecordRESTRequest("GET", "UNMATCHED", "4xx")

	snap := r.Snapshot()
	if snap.RESTEndpointCalls["GET /api/v1/servers"]["2xx"] != 2 {
		t.Errorf("GET /api/v1/servers 2xx = %d, want 2", snap.RESTEndpointCalls["GET /api/v1/servers"]["2xx"])
	}
	if snap.RESTEndpointCalls["GET /api/v1/servers"]["5xx"] != 1 {
		t.Errorf("GET /api/v1/servers 5xx = %d, want 1", snap.RESTEndpointCalls["GET /api/v1/servers"]["5xx"])
	}
	if snap.RESTEndpointCalls["POST /api/v1/servers/{name}/enable"]["2xx"] != 1 {
		t.Errorf("POST .../{name}/enable 2xx wrong")
	}
	if snap.RESTEndpointCalls["GET UNMATCHED"]["4xx"] != 1 {
		t.Errorf("UNMATCHED 4xx wrong")
	}
}

func TestRecordErrorRejectsUnknown(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordError(ErrCatOAuthRefreshFailed)
	r.RecordError(ErrCatOAuthRefreshFailed)
	r.RecordError(ErrorCategory("not_a_real_category"))
	r.RecordError(ErrorCategory(""))

	snap := r.Snapshot()
	if snap.ErrorCategoryCounts["oauth_refresh_failed"] != 2 {
		t.Errorf("oauth_refresh_failed = %d, want 2", snap.ErrorCategoryCounts["oauth_refresh_failed"])
	}
	if _, ok := snap.ErrorCategoryCounts["not_a_real_category"]; ok {
		t.Error("unknown error category leaked into snapshot")
	}
	if len(snap.ErrorCategoryCounts) != 1 {
		t.Errorf("expected exactly 1 error category, got %d", len(snap.ErrorCategoryCounts))
	}
}

type fakeDoctorResult struct {
	name string
	pass bool
}

func (f fakeDoctorResult) GetName() string { return f.name }
func (f fakeDoctorResult) IsPass() bool    { return f.pass }

func TestRecordDoctorRunAggregates(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordDoctorRun([]DoctorCheckResult{
		fakeDoctorResult{name: "db_writable", pass: true},
		fakeDoctorResult{name: "config_valid", pass: true},
		fakeDoctorResult{name: "port_available", pass: false},
	})
	r.RecordDoctorRun([]DoctorCheckResult{
		fakeDoctorResult{name: "db_writable", pass: true},
		fakeDoctorResult{name: "port_available", pass: true},
	})

	snap := r.Snapshot()
	if snap.DoctorChecks["db_writable"].Pass != 2 || snap.DoctorChecks["db_writable"].Fail != 0 {
		t.Errorf("db_writable = %+v, want pass=2 fail=0", snap.DoctorChecks["db_writable"])
	}
	if snap.DoctorChecks["port_available"].Pass != 1 || snap.DoctorChecks["port_available"].Fail != 1 {
		t.Errorf("port_available = %+v, want pass=1 fail=1", snap.DoctorChecks["port_available"])
	}
}

func TestSnapshotDoesNotResetCounters(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordSurface(SurfaceCLI)
	r.RecordBuiltinTool("retrieve_tools")
	r.RecordRESTRequest("GET", "/api/v1/status", "2xx")
	r.RecordError(ErrCatOAuthRefreshFailed)
	r.RecordUpstreamTool()

	_ = r.Snapshot()

	// Snapshot must not reset; second snapshot should reflect the same data.
	snap2 := r.Snapshot()
	if snap2.SurfaceCounts["cli"] != 1 {
		t.Errorf("cli surface lost across snapshots: %d", snap2.SurfaceCounts["cli"])
	}
	if snap2.BuiltinToolCalls["retrieve_tools"] != 1 {
		t.Error("builtin tool count lost across snapshots")
	}
	if snap2.RESTEndpointCalls["GET /api/v1/status"]["2xx"] != 1 {
		t.Error("REST endpoint count lost across snapshots")
	}
	if snap2.ErrorCategoryCounts["oauth_refresh_failed"] != 1 {
		t.Error("error category lost across snapshots")
	}
	if snap2.UpstreamToolCallCountBucket != "1-10" {
		t.Errorf("upstream bucket = %q, want 1-10", snap2.UpstreamToolCallCountBucket)
	}
}

func TestSnapshotIsImmutable(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordBuiltinTool("retrieve_tools")
	r.RecordRESTRequest("GET", "/api/v1/status", "2xx")

	snap := r.Snapshot()
	// Mutate the snapshot maps; future snapshots should not be affected.
	snap.BuiltinToolCalls["retrieve_tools"] = 999
	snap.RESTEndpointCalls["GET /api/v1/status"]["2xx"] = 999

	snap2 := r.Snapshot()
	if snap2.BuiltinToolCalls["retrieve_tools"] != 1 {
		t.Errorf("snapshot mutation leaked into registry: %d", snap2.BuiltinToolCalls["retrieve_tools"])
	}
	if snap2.RESTEndpointCalls["GET /api/v1/status"]["2xx"] != 1 {
		t.Errorf("snapshot REST mutation leaked: %d", snap2.RESTEndpointCalls["GET /api/v1/status"]["2xx"])
	}
}

func TestResetClearsAll(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordSurface(SurfaceCLI)
	r.RecordSurface(SurfaceTray)
	r.RecordBuiltinTool("retrieve_tools")
	r.RecordRESTRequest("GET", "/api/v1/status", "2xx")
	r.RecordError(ErrCatOAuthRefreshFailed)
	r.RecordUpstreamTool()
	r.RecordDoctorRun([]DoctorCheckResult{fakeDoctorResult{name: "x", pass: true}})

	r.Reset()
	snap := r.Snapshot()

	for _, s := range []string{"mcp", "cli", "webui", "tray", "unknown"} {
		if snap.SurfaceCounts[s] != 0 {
			t.Errorf("after reset: %s = %d", s, snap.SurfaceCounts[s])
		}
	}
	if len(snap.BuiltinToolCalls) != 0 {
		t.Errorf("after reset: builtin map not empty: %v", snap.BuiltinToolCalls)
	}
	if len(snap.RESTEndpointCalls) != 0 {
		t.Errorf("after reset: REST map not empty: %v", snap.RESTEndpointCalls)
	}
	if len(snap.ErrorCategoryCounts) != 0 {
		t.Errorf("after reset: error map not empty")
	}
	if len(snap.DoctorChecks) != 0 {
		t.Errorf("after reset: doctor map not empty")
	}
	if snap.UpstreamToolCallCountBucket != "0" {
		t.Errorf("after reset: upstream bucket = %q", snap.UpstreamToolCallCountBucket)
	}
}

func TestSortedOAuthProviderTypesDedupAndSort(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"github", "google", "github"}, []string{"github", "google"}},
		{[]string{"google", "github", "microsoft", "generic"}, []string{"generic", "github", "google", "microsoft"}},
	}
	for _, c := range cases {
		got := SortedOAuthProviderTypes(c.in)
		if len(got) != len(c.want) {
			t.Errorf("len = %d, want %d (%v vs %v)", len(got), len(c.want), got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("index %d: got %q want %q", i, got[i], c.want[i])
			}
		}
	}
}
