package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the durable, grep-proof half of #1158. A mechanical conversion of ~75
// URL log fields across two packages cannot be verified by eye, and the 76th
// site added next month re-opens the hole silently. So the rule is derived from
// the source rather than maintained by hand.
//
// It deliberately spans BOTH packages. Scoping it to internal/oauth — which is
// what the original design proposed — would green-light a fix that leaves every
// `auth_url` site in internal/upstream/core raw, and those are the highest
// blast-radius sinks in the issue: they fire at INFO on every OAuth login with
// no debug flag at all.

// urlFieldName matches the zap field NAMES whose values are URLs.
var urlFieldName = regexp.MustCompile(`(?i)(^url$|^urls$|_url$|_urls$|urls_tried|^resource$|_resource$|^endpoint$|_endpoint$)`)

// safeURLRenderer matches the call expressions that are an acceptable value for
// such a field. Anything else is a raw URL reaching a log sink.
var safeURLRenderer = regexp.MustCompile(
	`logSafeURL|logSafeURLs|logSafeAuthURL|RedactURL|RedactURLQueryParams|URLValue|URLValueDeep|ScrubUpstreamText|ExtraParamValue|MaskValue`)

type guardedPkg struct {
	dir  string
	name string
}

func TestNoRawURLReachesALogField(t *testing.T) {
	pkgs := []guardedPkg{
		{".", "internal/oauth"},
		{"../upstream/core", "internal/upstream/core"},
	}

	checked := 0
	for _, pkg := range pkgs {
		files, err := filepath.Glob(filepath.Join(pkg.dir, "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, files, "%s: the guard found no files to scan", pkg.name)

		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file) //nolint:gosec // fixed, in-repo paths
			require.NoError(t, err)
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, src, 0)
			require.NoError(t, err)

			ast.Inspect(parsed, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "zap" {
					return true
				}
				if sel.Sel.Name != "String" && sel.Sel.Name != "Strings" {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				name, err := strconv.Unquote(lit.Value)
				if err != nil || !urlFieldName.MatchString(name) {
					return true
				}
				checked++

				valueSrc := string(src[call.Args[1].Pos()-1 : call.Args[1].End()-1])
				// A string literal carries no operator data.
				if _, isLit := call.Args[1].(*ast.BasicLit); isLit {
					return true
				}
				assert.True(t, safeURLRenderer.MatchString(valueSrc),
					"%s: zap.%s(%q, %s) writes a raw URL to a log sink. Route it through "+
						"logSafeURL / logSafeAuthURL (issue #1158): a configured upstream URL "+
						"routinely carries ?token=, and log files outlive the process.",
					fset.Position(call.Pos()), sel.Sel.Name, name, valueSrc)
				return true
			})
		}
	}

	assert.Greater(t, checked, 50,
		"the guard matched only %d URL log fields — it has stopped seeing the sites it exists for", checked)
}

// TestAuthorizationURLIsNeverPrintedRaw covers the OTHER sink in the same file.
//
// internal/upstream/core/connection_oauth.go keeps THREE near-duplicate
// authorize-URL emission paths, and each writes the URL twice: a zap field and
// an fmt.Printf to the operator's terminal. A fix that patches only the zap
// half still puts the credential on stdout, and an observer-only test shows
// green against it.
func TestAuthorizationURLIsNeverPrintedRaw(t *testing.T) {
	const file = "../upstream/core/connection_oauth.go"
	src, err := os.ReadFile(file)
	require.NoError(t, err)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, src, 0)
	require.NoError(t, err)

	printfs := 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "fmt" || !strings.HasPrefix(sel.Sel.Name, "Print") {
			return true
		}
		for _, arg := range call.Args {
			ident, ok := arg.(*ast.Ident)
			if !ok || ident.Name != "authURL" {
				continue
			}
			printfs++
			assert.Fail(t,
				"the authorization URL is printed to stdout unredacted",
				"%s: fmt.%s prints the bare `authURL` identifier. It carries the configured "+
					"upstream URL as the RFC 8707 `resource` parameter, credentials included; "+
					"wrap it in logSafeAuthURL (issue #1158).",
				fset.Position(call.Pos()), sel.Sel.Name)
		}
		return true
	})
	assert.Zero(t, printfs)
}
