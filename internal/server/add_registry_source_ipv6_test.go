package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An IPv6 literal host contains colons. Cutting at the first ':' to drop a port
// reduced every such host to its first group, so distinct registries collided
// on the same derived id and the second one was refused as a duplicate.
func TestBuildRegistrySourceEntry_IPv6HostID(t *testing.T) {
	cases := map[string]string{
		"https://[2606:4700:4700::1111]/":     "2606-4700-4700--1111",
		"https://[2606:4700::64]:8443/":       "2606-4700--64",
		"https://www.Registry.Example:8443/":  "registry-example",
		"https://registry.acme.example/v0.1/": "registry-acme-example",
	}
	for raw, want := range cases {
		entry, err := buildRegistrySourceEntry(raw, "", "", "")
		require.NoError(t, err, raw)
		assert.Equal(t, want, entry.ID, raw)
	}
}
