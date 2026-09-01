package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The upgrade path for #1143 rests entirely on this: a config file written
// before the git-capable key existed persists its own `default_images` map, and
// encoding/json decodes an object into the ALREADY-POPULATED default map rather
// than replacing it. The built-in entry therefore survives the merge, and the
// operator's own entries win where they overlap.
//
// This is also why the resolver does not need to hardcode a public image as its
// upgrade fallback, and why a partial `default_images` in a config file does
// not blank out the runtimes it omits.
func TestDefaultImagesMergeOverBuiltInsOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFileName)
	legacy := `{
	  "listen": "127.0.0.1:8080",
	  "data_dir": ` + jsonString(dir) + `,
	  "docker_isolation": {
	    "enabled": true,
	    "default_images": {
	      "uvx": "mirror.internal/astral/uv:slim",
	      "python": "mirror.internal/astral/uv:slim",
	      "npx": "mirror.internal/library/node:22"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	images := cfg.DockerIsolation.DefaultImages

	if got := images["uvx"]; got != "mirror.internal/astral/uv:slim" {
		t.Errorf("operator's uvx entry = %q, want it to win the merge", got)
	}
	if got := images[GitCapableImageKey]; got != DefaultGitCapableImage {
		t.Errorf("default_images[%q] = %q, want the built-in %q seeded by the merge",
			GitCapableImageKey, got, DefaultGitCapableImage)
	}
	if got := images["ruby"]; got == "" {
		t.Error("a runtime the config file omits lost its built-in image — the map was replaced, not merged")
	}
}

func jsonString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '"' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(append(out, '"'))
}
