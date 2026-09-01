package config

import "testing"

// The shipped Python default image is the SLIM uv image, which has no git:
//
//	$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm-slim -c 'git --version'
//	sh: 1: git: not found
//	$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm    -c 'git --version'
//	git version 2.39.5
//
// So every `uvx --from …git+https://…` server fails under Docker isolation
// (#1143). The git-capable image is named by its own default_images key so
// operators can retarget it instead of us bumping a ~1.5GB image on everyone.
func TestDefaultDockerIsolationConfig_HasGitCapableKey(t *testing.T) {
	images := DefaultDockerIsolationConfig().DefaultImages

	got, ok := images[GitCapableImageKey]
	if !ok {
		t.Fatalf("default_images has no %q key; keys=%v", GitCapableImageKey, images)
	}
	if got != DefaultGitCapableImage {
		t.Errorf("default_images[%q] = %q, want %q", GitCapableImageKey, got, DefaultGitCapableImage)
	}
	// The git-capable default must not be the slim image it exists to replace.
	if got == images["uvx"] {
		t.Errorf("git-capable default equals the (git-less) uvx default %q", got)
	}
}

func TestNeedsGitCapableImage(t *testing.T) {
	tests := []struct {
		name        string
		runtimeType string
		args        []string
		want        bool
	}{
		{"uvx --from git+https", "uvx", []string{"--from", "git+https://github.com/o/r", "srv"}, true},
		{"uvx pkg@git+https", "uvx", []string{"--from", "pkg@git+https://github.com/o/r@main", "pkg"}, true},
		{"uvx git+ssh", "uvx", []string{"--from", "git+ssh://git@github.com/o/r", "srv"}, true},
		{"pip install git+", "pip", []string{"install", "git+https://github.com/o/r"}, true},
		{"pipx git+", "pipx", []string{"run", "--spec", "git+https://github.com/o/r", "srv"}, true},
		{"python -m pip git+", "python", []string{"-m", "pip", "install", "git+https://github.com/o/r"}, true},
		{"uppercase GIT+ still matches", "uvx", []string{"--from", "GIT+HTTPS://github.com/o/r"}, true},
		{"plain uvx package", "uvx", []string{"mcp-server-fetch"}, false},
		{"uvx package merely named git", "uvx", []string{"mcp-git"}, false},
		// node:22 already ships git (verified: git version 2.39.5), so npx git
		// deps work today — swapping its image would be a pointless 1.6GB
		// regression.
		{"npx git+ needs no swap", "npx", []string{"git+https://github.com/o/r"}, false},
		{"node git+ needs no swap", "node", []string{"git+https://github.com/o/r"}, false},
		{"no args", "uvx", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsGitCapableImage(tc.runtimeType, tc.args); got != tc.want {
				t.Errorf("NeedsGitCapableImage(%q, %v) = %v, want %v", tc.runtimeType, tc.args, got, tc.want)
			}
		})
	}
}
