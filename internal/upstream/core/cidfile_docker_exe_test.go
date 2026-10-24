package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Windows shell wrap names the CLI docker.exe, often as a quoted path
// (Docker Desktop installs under "Program Files"). --cidfile has to land
// immediately after "run". Appending it as extra shell argv leaves docker
// without a cidfile, so the container cannot be tracked for cleanup.
func TestInsertCidfile_DockerExe(t *testing.T) {
	c := newIsolatedTestClient()
	const cid = `C:\Temp\mcpproxy-cid.txt`

	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "bare docker.exe",
			cmd:  `docker.exe run --rm -i ghcr.io/some/image python -m srv`,
			want: `docker.exe run --cidfile ` + cid + ` --rm -i ghcr.io/some/image python -m srv`,
		},
		{
			name: "quoted docker.exe path",
			cmd:  `"C:\Program Files\Docker\resources\bin\docker.exe" run --rm -i ghcr.io/some/image`,
			want: `"C:\Program Files\Docker\resources\bin\docker.exe" run --cidfile ` + cid + ` --rm -i ghcr.io/some/image`,
		},
		{
			name: "unix docker still inserts after run",
			cmd:  `docker run --rm -i ghcr.io/some/image`,
			want: `docker run --cidfile ` + cid + ` --rm -i ghcr.io/some/image`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []string{"/c", tc.cmd}
			out := c.insertCidfileIntoShellDockerCommand(in, cid)
			require.Len(t, out, len(in), "cidfile must be inside the command string, not extra shell argv: %v", out)
			assert.Equal(t, tc.want, out[len(out)-1])
		})
	}

	// A command that merely mentions docker must not be rewritten.
	out := c.insertCidfileIntoShellDockerCommand([]string{"/c", "echo docker run"}, cid)
	assert.False(t, strings.Contains(out[len(out)-1], "--cidfile"))
}
