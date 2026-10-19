package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteYAML(t *testing.T) {
	in := "    req:\n      properties:\n        hashes:\n          additionalProperties:\n            type: string\n          description: |-\n            text\n          type: object\n          x-nullable: true\n        other:\n          type: boolean\n      type: object\n"
	want := "    req:\n      properties:\n        hashes:\n          additionalProperties:\n            type: string\n          description: |-\n            text\n          type:\n          - object\n          - \"null\"\n        other:\n          type: boolean\n      type: object\n"
	got, err := rewriteYAML(in)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	again, err := rewriteYAML(got)
	require.NoError(t, err)
	assert.Equal(t, got, again, "idempotent")
}

func TestRewriteYAML_MarkerWithoutTypeFails(t *testing.T) {
	_, err := rewriteYAML("  a:\n    type: object\n  b:\n    x-nullable: true\n")
	require.Error(t, err, "a marker must not borrow a type from another mapping")
}

func TestRewriteJSON(t *testing.T) {
	in := `{"h":{"additionalProperties":{"type":"string"},"description":"x","type":"object","x-nullable":true},"a":{"type":"array","uniqueItems":false,"x-nullable":true,"x-other":1}}`
	want := `{"h":{"additionalProperties":{"type":"string"},"description":"x","type":["object","null"]},"a":{"type":["array","null"],"uniqueItems":false,"x-other":1}}`
	got, err := rewriteJSON(in)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = rewriteJSON(`{"x-nullable":true,"type":"object"}`)
	require.Error(t, err, "an unconvertible marker must fail loudly")
}
