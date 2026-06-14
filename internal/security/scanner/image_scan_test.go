package scanner

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestDockerImageFromCommand(t *testing.T) {
	cases := []struct {
		name string
		info ServerInfo
		want string
	}{
		{
			name: "simple docker run",
			info: ServerInfo{Command: "docker", Args: []string{"run", "-i", "--rm", "mcp/fetch"}},
			want: "mcp/fetch",
		},
		{
			name: "value flags before image, command after",
			info: ServerInfo{Command: "docker", Args: []string{"run", "--rm", "-i", "-e", "FOO=bar", "ghcr.io/x/y:tag", "serve"}},
			want: "ghcr.io/x/y:tag",
		},
		{
			name: "podman with volume",
			info: ServerInfo{Command: "podman", Args: []string{"run", "-v", "/tmp:/data", "mcp/time"}},
			want: "mcp/time",
		},
		{
			name: "name and attached long flag",
			info: ServerInfo{Command: "docker", Args: []string{"run", "--name", "x", "--network=host", "mcp/git"}},
			want: "mcp/git",
		},
		{
			name: "absolute docker path",
			info: ServerInfo{Command: "/usr/local/bin/docker", Args: []string{"run", "alpine"}},
			want: "alpine",
		},
		{
			name: "attached short env flag with equals",
			info: ServerInfo{Command: "docker", Args: []string{"run", "-e", "A=1", "-eB=2", "mcp/x"}},
			want: "mcp/x",
		},
		{
			name: "combined boolean short flags",
			info: ServerInfo{Command: "docker", Args: []string{"run", "-it", "mcp/x"}},
			want: "mcp/x",
		},
		{
			name: "docker container run subcommand",
			info: ServerInfo{Command: "docker", Args: []string{"container", "run", "mcp/x"}},
			want: "mcp/x",
		},
		{
			name: "not a docker command",
			info: ServerInfo{Command: "uvx", Args: []string{"mcp-server"}},
			want: "",
		},
		{
			name: "docker but not run",
			info: ServerInfo{Command: "docker", Args: []string{"ps"}},
			want: "",
		},
		{
			name: "no image present",
			info: ServerInfo{Command: "docker", Args: []string{"run", "--rm"}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerImageFromCommand(tc.info); got != tc.want {
				t.Errorf("dockerImageFromCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveDockerRunImage(t *testing.T) {
	r := NewSourceResolver(zap.NewNop())
	resolved, err := r.Resolve(context.Background(), ServerInfo{
		Name:     "fetch",
		Protocol: "stdio",
		Command:  "docker",
		Args:     []string{"run", "-i", "--rm", "mcp/fetch"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	defer resolved.Cleanup()
	if resolved.Method != "container_image" {
		t.Errorf("Method = %q, want container_image", resolved.Method)
	}
	if resolved.ContainerImage != "mcp/fetch" {
		t.Errorf("ContainerImage = %q, want mcp/fetch", resolved.ContainerImage)
	}
	if resolved.SourceDir != "" {
		t.Errorf("SourceDir = %q, want empty", resolved.SourceDir)
	}
}

func TestScannerCommandForImage(t *testing.T) {
	trivy := &ScannerPlugin{
		ID:           "trivy-mcp",
		Inputs:       []string{"source", "container_image"},
		Command:      []string{"fs", "--format", "sarif", "/scan/source"},
		ImageCommand: []string{"image", "--format", "sarif", "{{IMAGE}}"},
	}

	// Image present + scanner supports it → image-mode command with substitution.
	got := effectiveScannerCommand(trivy, ScanRequest{ContainerImage: "mcp/fetch"})
	want := []string{"image", "--format", "sarif", "mcp/fetch"}
	if !equalSlice(got, want) {
		t.Errorf("image-mode command = %v, want %v", got, want)
	}

	// No image → fall back to default source-mode command.
	got = effectiveScannerCommand(trivy, ScanRequest{})
	if !equalSlice(got, trivy.Command) {
		t.Errorf("no-image command = %v, want %v", got, trivy.Command)
	}

	// Scanner without container_image support → keep its command even if an image is present.
	semgrep := &ScannerPlugin{
		ID:      "semgrep-mcp",
		Inputs:  []string{"source"},
		Command: []string{"semgrep", "scan", "/scan/source"},
	}
	got = effectiveScannerCommand(semgrep, ScanRequest{ContainerImage: "mcp/fetch"})
	if !equalSlice(got, semgrep.Command) {
		t.Errorf("non-image scanner command = %v, want %v", got, semgrep.Command)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
