package oauth

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestURLValueDeep_MasksTheNestedResourceURL is the guard for the biggest leak
// in #1158.
//
// autoDetectResource returns the CONFIGURED upstream URL verbatim on every
// fallback branch, that value becomes extraParams["resource"], and every
// authorize-URL builder splices each extra param into the query — so the
// authorize URL that is logged at INFO and printed to stdout on every login
// attempt carries `resource=<percent-encoded configured URL with its ?token=>`.
//
// The percent-encoding is what makes this its own rule: neither the
// sensitive-query-parameter NAME rule (which sees one opaque value under the
// name `resource`) nor secretPattern (which needs a literal `=` after `token`,
// not `%3D`) can see the credential at that nesting level.
func TestURLValueDeep_MasksTheNestedResourceURL(t *testing.T) {
	const secret = "SUPERSECRETALPHAQUERYVALUE"
	upstream := "https://host.example.com/mcp?token=" + secret
	authURL := "https://auth.example.com/authorize?client_id=abc123&resource=" +
		url.QueryEscape(upstream) + "&state=xyz"

	// The rule the design originally proposed is NOT sufficient — pin that, so
	// nobody "simplifies" URLValueDeep back to it.
	assert.Contains(t, RedactURLQueryParams(authURL), secret,
		"sanity: the name-rule-only helper cannot see a credential nested inside a "+
			"percent-encoded parameter value; if it can, this test is no longer testing anything")

	got := AuditRedaction.URLValueDeep(authURL)
	assert.NotContains(t, got, secret, "the nested upstream credential reached the log field")
	assert.NotContains(t, got, url.QueryEscape(secret))

	// Diagnostics survive: an operator must still see WHICH provider and
	// WHICH resource the flow was for.
	assert.Contains(t, got, "auth.example.com/authorize")
	assert.Contains(t, got, "client_id=abc123")
	assert.Contains(t, got, "state=xyz")
	assert.Contains(t, got, "host.example.com/mcp",
		"the resource endpoint's host and path must survive; only its credential goes")
}

// TestURLValueDeep_MasksTheTopLevelQueryToo — the nesting rule must not replace
// the ordinary one.
func TestURLValueDeep_MasksTheTopLevelQueryToo(t *testing.T) {
	const secret = "SUPERSECRETALPHAQUERYVALUE"
	got := AuditRedaction.URLValueDeep("https://host.example.com/mcp?token=" + secret)
	assert.NotContains(t, got, secret)
	assert.Contains(t, got, "host.example.com/mcp")
}

// TestExtraParamValue_MasksResourceCredentials guards the sibling Debug line:
// the authorize-URL builders log every extra parameter's VALUE, and
// maskExtraParams deliberately showed `resource` in full because it was
// documented as "a public endpoint". It is not — it is the configured URL.
func TestExtraParamValue_MasksResourceCredentials(t *testing.T) {
	const secret = "SUPERSECRETALPHAQUERYVALUE"
	upstream := "https://host.example.com/mcp?token=" + secret

	got := AuditRedaction.ExtraParamValue("resource", upstream)
	assert.NotContains(t, got, secret)
	assert.Contains(t, got, "host.example.com/mcp")

	masked := maskExtraParams(map[string]string{"resource": upstream})
	assert.NotContains(t, masked["resource"], secret,
		"maskExtraParams showed `resource` in full; it carries the configured URL")
}

// TestCreateMetadataError_ScrubsEveryURLLeaf reflect-walks the built error so a
// string field added later is covered without editing this test.
func TestCreateMetadataError_ScrubsEveryURLLeaf(t *testing.T) {
	const secret = "SUPERSECRETALPHAQUERYVALUE"
	serverURL := "https://host.example.com/mcp?token=" + secret
	prmURL := "https://host.example.com/.well-known/oauth-protected-resource?token=" + secret
	asURL := "https://host.example.com/.well-known/oauth-authorization-server?token=" + secret

	err := createMetadataError("alpha", serverURL, &OAuthMetadataValidationResult{
		ProtectedResourceMetadataURL:         prmURL,
		ProtectedResourceError:               &urlBearingError{msg: `Get "` + prmURL + `": 404`},
		AuthorizationServerMetadataURL:       asURL,
		AuthorizationServerMetadataURLsTried: []string{asURL, prmURL},
		AuthorizationServerError:             &urlBearingError{msg: `Get "` + asURL + `": 404`},
	})
	require.Error(t, err)

	var leaves []string
	collectStrings(reflect.ValueOf(err), &leaves)
	require.NotEmpty(t, leaves)

	sawEndpoint := false
	for _, leaf := range leaves {
		assert.NotContains(t, leaf, secret,
			"a URL-derived leaf of the structured OAuth error is JSON-encoded straight to the client")
		if strings.Contains(leaf, "host.example.com") {
			sawEndpoint = true
		}
	}
	assert.True(t, sawEndpoint,
		"positive control: the endpoint host must SURVIVE. A blanked field would pass the "+
			"leak assertion above while destroying the operator's ability to see which URL failed")
}

type urlBearingError struct{ msg string }

func (e *urlBearingError) Error() string { return e.msg }

// collectStrings walks every reachable string leaf of v.
func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		if v.String() != "" {
			*out = append(*out, v.String())
		}
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			collectStrings(v.Elem(), out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectStrings(v.Index(i), out)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			collectStrings(v.MapIndex(k), out)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				continue
			}
			collectStrings(f, out)
		}
	default:
	}
}
