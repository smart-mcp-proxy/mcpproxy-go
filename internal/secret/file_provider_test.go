package secret

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileProvider_Resolve(t *testing.T) {
	p := NewFileProvider()
	assert.True(t, p.CanResolve("file"))
	assert.False(t, p.CanResolve("env"))
	assert.True(t, p.IsAvailable())

	path := writeSecretFile(t, "tok-123\n")
	got, err := p.Resolve(context.Background(), Ref{Type: "file", Name: path})
	require.NoError(t, err)
	assert.Equal(t, "tok-123", got)

	_, err = p.Resolve(context.Background(), Ref{Type: "env", Name: path})
	require.Error(t, err)

	_, err = p.Resolve(context.Background(), Ref{Type: "file", Name: writeSecretFile(t, "\n")})
	require.Error(t, err, "an empty secret is an error, not an empty credential")

	require.Error(t, p.Store(context.Background(), Ref{Type: "file", Name: path}, "x"))
	require.Error(t, p.Delete(context.Background(), Ref{Type: "file", Name: path}))
	refs, err := p.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestFileProvider_ReadsTheFileEachTime(t *testing.T) {
	p := NewFileProvider()
	path := writeSecretFile(t, "v1")
	ref := Ref{Type: "file", Name: path}

	got, err := p.Resolve(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "v1", got)

	require.NoError(t, os.WriteFile(path, []byte("v2"), 0o600))
	got, err = p.Resolve(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "v2", got, "a rotated secret is picked up on the next resolve")
}

func TestResolver_ExpandsFileReferences(t *testing.T) {
	r := NewResolver()
	lf := writeSecretFile(t, "same-value\n")
	crlf := writeSecretFile(t, "same-value\r\n")

	a, err := r.ExpandSecretRefs(context.Background(), "${file:"+lf+"}")
	require.NoError(t, err)
	b, err := r.ExpandSecretRefs(context.Background(), "${file:"+crlf+"}")
	require.NoError(t, err)
	assert.Equal(t, "same-value", a)
	assert.Equal(t, a, b, "an LF-terminated and a CRLF-terminated file resolve to the identical value")

	embedded, err := r.ExpandSecretRefs(context.Background(), "Bearer ${file:"+lf+"}")
	require.NoError(t, err)
	assert.Equal(t, "Bearer same-value", embedded)
}

func TestResolver_FileReferenceErrorsNameTheReferenceNotTheValue(t *testing.T) {
	r := NewResolver()
	const sentinel = "SENTINEL-p3Wv9Qd"

	missing := filepath.Join(t.TempDir(), "missing")
	_, err := r.ExpandSecretRefs(context.Background(), "${file:"+missing+"}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "${file:"+missing+"}")

	_, err = r.ExpandSecretRefs(context.Background(), "${file:relative/path}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be absolute")

	tooBig := writeSecretFile(t, sentinel+strings.Repeat("a", MaxSecretFileBytes))
	_, err = r.ExpandSecretRefs(context.Background(), "${file:"+tooBig+"}")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), sentinel)
}

func TestParseSecretRef_FilePaths(t *testing.T) {
	for _, path := range []string{`/run/secrets/token`, `/run/credentials/mcpproxy.service/key`, `C:\ProgramData\mcpproxy\token`} {
		ref, err := ParseSecretRef("${file:" + path + "}")
		require.NoError(t, err, path)
		assert.Equal(t, "file", ref.Type)
		assert.Equal(t, path, ref.Name)
	}
}
