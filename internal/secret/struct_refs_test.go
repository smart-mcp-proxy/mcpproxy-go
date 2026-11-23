package secret

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

type refsInner struct {
	Path string
	List []string
}

type refsOpaque struct {
	raw string //nolint:unused // stands in for an opaque carrier the resolver never walks
}

type refsSample struct {
	Env    map[string]string
	Any    map[string]interface{}
	Args   []string
	Inner  *refsInner
	Opaque *refsOpaque
	hidden string //nolint:unused // unexported: the resolver skips it
}

// StructFileRefPaths must see exactly the fields the resolver expands.
func TestStructFileRefPaths_MatchesResolverWalk(t *testing.T) {
	newSample := func() *refsSample {
		return &refsSample{
			Env:    map[string]string{"A": "${file:/a}", "B": "plain"},
			Any:    map[string]interface{}{"C": "${file:/c}", "N": 3},
			Args:   []string{"--x", "pre ${file:/d} post"},
			Inner:  &refsInner{Path: "${file:/e}", List: []string{"${file:/f}"}},
			Opaque: &refsOpaque{raw: "${file:/opaque}"},
			hidden: "${file:/hidden}",
		}
	}

	got := StructFileRefPaths(newSample())
	sort.Strings(got)
	assert.Equal(t, []string{"/a", "/c", "/d", "/e", "/f"}, got)

	// The resolver, with ${file:} disabled, reports one error per field it
	// tried; the same paths must come back.
	r := NewResolverWithOptions(ResolverOptions{Scope: ScopeUpstream, FilePolicy: NewDisabledFilePolicy(ScopeUpstream)})
	var fromResolver []string
	for _, e := range r.ExpandStructSecretsCollectErrors(context.Background(), newSample()) {
		for _, ref := range FindSecretRefs(e.Reference) {
			if ref.Type == SecretTypeFile {
				fromResolver = append(fromResolver, ref.Name)
			}
		}
	}
	sort.Strings(fromResolver)
	assert.Equal(t, got, fromResolver)
}
