package core

import "testing"

func TestBuildFullImageName_ConfiguredRegistryPrefixesNamespacedImages(t *testing.T) {
	mirror := registryOnlyManager("mirror.corp", nil)
	cases := map[string]string{
		"myorg/node-mcp:1":              "mirror.corp/myorg/node-mcp:1",
		"python:3.11":                   "mirror.corp/library/python:3.11",
		"ghcr.io/org/img:2":             "ghcr.io/org/img:2",
		"localhost:5000/team/img":       "localhost:5000/team/img",
		"localhost/team/img":            "localhost/team/img",
		"registry.local/img@sha256:abc": "registry.local/img@sha256:abc",
	}
	for in, want := range cases {
		if got := mirror.buildFullImageName(in); got != want {
			t.Errorf("registry=mirror.corp: buildFullImageName(%q) = %q, want %q", in, got, want)
		}
	}

	plain := registryOnlyManager("", nil)
	for in, want := range map[string]string{
		"myorg/node-mcp:1": "myorg/node-mcp:1",
		"python:3.11":      "docker.io/library/python:3.11",
	} {
		if got := plain.buildFullImageName(in); got != want {
			t.Errorf("no registry: buildFullImageName(%q) = %q, want %q", in, got, want)
		}
	}
}
