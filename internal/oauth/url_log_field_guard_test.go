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
//
// Review round 2 (finding B2) widened it. The first cut matched the LITERAL
// `urls_tried`, so the sibling `url_tried` four lines below a site it had just
// fixed sailed through, and so did every `auth_server*` field in
// discovery.go — five names, all of them holding a URL derived from the
// configured upstream URL. A guard whose regex is written from the sites that
// were already fixed proves nothing about the ones that were not.
var urlFieldName = regexp.MustCompile(`(?i)(` + strings.Join([]string{
	`^url$`, `^urls$`, `_url$`, `_urls$`,
	`_tried$`, // url_tried, urls_tried, metadata_urls_tried, …
	`^resource$`, `_resource$`,
	`^endpoint$`, `_endpoint$`,
	`^auth_server$`, `^auth_servers$`, `^auth_server_base$`,
	`^authorization_servers$`,
}, "|") + `)`)

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

// TestNoRawErrorSitsBesideAMaskedURL is the other half of the same rule
// (issue #1158, review round 2, finding B3).
//
// Masking a statement's `url` field does not mask the statement. A net/http
// error quotes the request URL inside its own message —
//
//	Post "https://host/mcp?token=SECRET": dial tcp 10.0.0.1:443: i/o timeout
//
// — so `zap.Error(err)` sitting next to `zap.String("metadata_url",
// logSafeURL(u))` publishes, on the identical log line, the credential its
// neighbour was written to hide. internal/oauth/discovery.go had fourteen of
// these, every one on an HTTP failure path.
//
// The rule is therefore structural rather than per-site: on a log statement
// that carries a URL field at all, the error goes through the scrubbing
// renderer (logSafeErrorField / oauth.LogSafeErrorText), never zap.Error.
func TestNoRawErrorSitsBesideAMaskedURL(t *testing.T) {
	pkgs := []guardedPkg{
		{".", "internal/oauth"},
		{"../upstream/core", "internal/upstream/core"},
		{"../transport", "internal/transport"},
	}

	logMethod := map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true, "Fatal": true, "Panic": true}
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
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !logMethod[sel.Sel.Name] {
					return true
				}

				carriesURL := false
				var rawErrors []string
				for _, arg := range call.Args {
					argSrc := string(src[arg.Pos()-1 : arg.End()-1])
					if name, ok := zapFieldName(arg); ok && urlFieldName.MatchString(name) {
						carriesURL = true
					}
					if safeURLRenderer.MatchString(argSrc) {
						carriesURL = true
					}
					if isZapErrorField(arg) {
						rawErrors = append(rawErrors, argSrc)
					}
				}
				if !carriesURL {
					return true
				}
				checked++
				for _, raw := range rawErrors {
					assert.Fail(t,
						"a raw error sits on a log statement whose URL is masked",
						"%s: %s writes a URL through the safe renderer and then hands the same "+
							"line `%s`. A *url.Error renders the request URL verbatim inside its "+
							"own text, so the credential the sibling field masks reaches the log "+
							"anyway. Use logSafeErrorField / oauth.LogSafeErrorText (issue #1158).",
						fset.Position(call.Pos()), sel.Sel.Name, raw)
				}
				return true
			})
		}
	}

	assert.Greater(t, checked, 20,
		"the guard matched only %d URL-bearing log statements — it has stopped seeing the sites it exists for", checked)
}

// zapFieldName returns the literal field name of a `zap.String("x", …)` /
// `zap.Strings("x", …)` argument.
func zapFieldName(arg ast.Expr) (string, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "zap" {
		return "", false
	}
	if sel.Sel.Name != "String" && sel.Sel.Name != "Strings" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return name, true
}

// isZapErrorField reports whether an argument is zap.Error(…) / zap.NamedError(…).
func isZapErrorField(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "zap" {
		return false
	}
	return sel.Sel.Name == "Error" || sel.Sel.Name == "NamedError"
}

// TestEveryOAuthFlowErrorIsScrubbedAtConstruction pins finding B7.
//
// contracts.OAuthFlowError is JSON-encoded straight to the REST caller. Its
// leaves were scrubbed one field at a time at eight construction sites, and one
// of them got it half right: emptyClientIDFlowError masked Details.ServerURL
// and set Details.DCRStatus.Error raw on the same struct. Per-field scrubbing
// at N sites is how that happens, so the rule is now "every literal goes
// through scrubbedFlowError" — and that is checkable.
func TestEveryOAuthFlowErrorIsScrubbedAtConstruction(t *testing.T) {
	files, err := filepath.Glob("../upstream/core/*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	literals := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file) //nolint:gosec // fixed, in-repo paths
		require.NoError(t, err)
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, src, 0)
		require.NoError(t, err)

		// Collect the positions of every literal that IS an argument of
		// scrubbedFlowError, then require every literal to be one of them.
		wrapped := map[token.Pos]bool{}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "scrubbedFlowError" || len(call.Args) != 1 {
				return true
			}
			arg := call.Args[0]
			if unary, ok := arg.(*ast.UnaryExpr); ok {
				arg = unary.X
			}
			wrapped[arg.Pos()] = true
			return true
		})

		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OAuthFlowError" {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "contracts" {
				return true
			}
			literals++
			assert.True(t, wrapped[lit.Pos()],
				"%s: this contracts.OAuthFlowError literal is not wrapped in scrubbedFlowError. "+
					"The struct is JSON-encoded to the REST caller; scrubbing its leaves per site "+
					"is what let DCRStatus.Error ship raw next to a masked ServerURL (issue #1158).",
				fset.Position(lit.Pos()))
			return true
		})
	}

	assert.GreaterOrEqual(t, literals, 7,
		"the guard matched only %d OAuthFlowError literals — it has stopped seeing the sites it exists for", literals)
}
