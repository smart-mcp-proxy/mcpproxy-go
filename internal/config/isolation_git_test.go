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
//
// The key is NOT seeded into the shipped map. SaveConfig marshals the whole
// merged map back into the operator's file, so any key mcpproxy seeds reappears
// there as if they had typed it — which is why presence, not value equality, is
// what says "the operator chose this image" (see DefaultGitCapableImage).
func TestDefaultDockerIsolationConfig_DoesNotSeedGitCapableKey(t *testing.T) {
	images := DefaultDockerIsolationConfig().DefaultImages

	if got, ok := images[GitCapableImageKey]; ok {
		t.Fatalf("default_images seeds %q = %q; presence can no longer mean operator intent",
			GitCapableImageKey, got)
	}
	// The built-in git-capable image must still not be the slim image it exists
	// to replace.
	if DefaultGitCapableImage == images["uvx"] {
		t.Errorf("built-in git-capable image equals the (git-less) uvx default %q", images["uvx"])
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
