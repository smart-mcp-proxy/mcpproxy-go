package telemetry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The tray writes its autostart sidecar under the instance root, which
// MCPPROXY_HOME relocates (GH #936). A core told `--data-dir <root>` therefore
// has to read <root>/tray-autostart.json — reading ~/.mcpproxy instead makes a
// QA instance report the real install's login-item state as its own, and leaves
// the sidecar the tray actually wrote as dead data.
func TestAutostartReaderForDataDirReadsTheInstanceRootSidecar(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("no tray sidecar on Linux by design; Read() always yields nil")
	}
	root := t.TempDir()
	writeSidecar(t, filepath.Join(root, autostartSidecarName), `{"enabled":true}`)

	got := AutostartReaderForDataDir(root).Read()
	if got == nil {
		t.Fatal("reader ignored the data directory and found no sidecar")
	}
	if !*got {
		t.Fatalf("sidecar says enabled:true, reader says %v", *got)
	}
}

// …and the relocated reader must not fall back to the production sidecar when
// the instance root has none: "this instance has no sidecar" is `unknown`, not
// "whatever the real install says".
func TestAutostartReaderForDataDirDoesNotFallBackToTheRealHome(t *testing.T) {
	root := t.TempDir()
	r := AutostartReaderForDataDir(root)
	if want := filepath.Join(root, autostartSidecarName); r.Path != want {
		t.Fatalf("reader path = %q, want %q", r.Path, want)
	}
	if got := r.Read(); got != nil {
		t.Fatalf("no sidecar in the instance root must read as unknown, got %v", *got)
	}
}

// An empty data directory keeps the historical behaviour: ~/.mcpproxy.
func TestAutostartReaderForDataDirFallsBackToHomeWhenUnset(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	r := AutostartReaderForDataDir("")
	if want := filepath.Join(home, ".mcpproxy", autostartSidecarName); r.Path != want {
		t.Fatalf("reader path = %q, want %q", r.Path, want)
	}
}

func writeSidecar(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}
