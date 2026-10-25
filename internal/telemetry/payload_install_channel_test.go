package telemetry

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func stubInstallChannel(t *testing.T, ch InstallChannel) *int {
	t.Helper()
	calls := 0
	prev := installChannelResolver
	installChannelResolver = func(version, dataDir string) InstallChannel {
		calls++
		return ch
	}
	t.Cleanup(func() { installChannelResolver = prev })
	return &calls
}

// Spec 121 US4.1: an enabled heartbeat carries install_channel, drawn from the
// closed set, and still passes the anonymity scanner.
func TestPayload_InstallChannelPresentAndScannable(t *testing.T) {
	stubInstallChannel(t, InstallChannelWinget)
	svc := newFunnelPayloadTestService(t)
	payload := svc.BuildPayload()
	if payload.InstallChannel != "winget" {
		t.Fatalf("InstallChannel = %q, want winget", payload.InstallChannel)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"install_channel":"winget"`) {
		t.Errorf("serialized payload missing install_channel: %s", b)
	}
	if err := ScanForPII(b); err != nil {
		t.Errorf("ScanForPII rejected a legitimate install_channel: %v", err)
	}
}

// Every member of the enum is accepted; anything else is rejected before send.
func TestAnonymityScanner_InstallChannelEnum(t *testing.T) {
	for _, c := range AllInstallChannels() {
		raw := `{"install_channel":"` + string(c) + `"}`
		if err := ScanForPII([]byte(raw)); err != nil {
			t.Errorf("enum member %q rejected: %v", c, err)
		}
	}
	for _, bad := range []string{`"flatpak"`, `"C:\\Users\\secret"`, `"/etc/passwd"`, `""`, `42`, `true`, `{"a":1}`} {
		if err := ScanForPII([]byte(`{"install_channel":` + bad + `}`)); err == nil {
			t.Errorf("install_channel=%s must be rejected by the scanner", bad)
		}
	}
}

// Spec 121 US4.6/FR-031: when telemetry is disabled neither a heartbeat nor a
// payload preview may read a channel marker.
func TestPayload_InstallChannelNotResolvedWhenTelemetryDisabled(t *testing.T) {
	calls := stubInstallChannel(t, InstallChannelBrew)
	svc := newFunnelPayloadTestService(t)
	off := false
	svc.config.Telemetry.Enabled = &off
	payload := svc.BuildPayload()
	if payload.InstallChannel != "" {
		t.Errorf("InstallChannel = %q while telemetry disabled, want empty", payload.InstallChannel)
	}
	if *calls != 0 {
		t.Errorf("resolver called %d times while telemetry disabled, want 0", *calls)
	}
}

// Privacy canary: a hostile marker or env value never reaches the payload.
func TestResolveInstallChannel_HostileValuesNeverEscape(t *testing.T) {
	f := fakeChannelDeps{
		env:      `C:\Users\secret\.ssh`,
		exeDir:   "/opt/app",
		dataDir:  "/data",
		detected: "../../etc/passwd",
		files: map[string]string{
			filepath.Join("/opt/app", "install-channel"): `C:\Users\secret`,
			filepath.Join("/data", "install-channel"):    strings.Repeat("A", 4096),
		},
	}
	got := resolveInstallChannel(f.deps())
	if got != InstallChannelUnknown {
		t.Fatalf("hostile inputs resolved to %q, want unknown", got)
	}
	if !got.Valid() {
		t.Fatal("result must always be a member of the closed set")
	}
}
