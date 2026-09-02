package oauth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1148, round 8 finding 4.
//
// Round 7 replaced a hand-maintained MARKER list with a hand-maintained
// RENDERER list (maskRenderings) and derived the markers from it. That removed
// one way to fall behind and left the other: nothing failed when a renderer was
// ADDED to this package and not registered, so the fail-closed net would keep
// accepting an echo of a rendering it had never been told about — which is
// exactly the shape of round 7's own defect (`***REDACTED***`).
//
// The gate below closes it structurally. The package's own SOURCE is parsed to
// discover every exported `func(string) string` — every function shaped like a
// mask rendering — and the test fails unless each discovered name is either
// BOUND into exportedStringFuncs (where its output is probed against the net)
// or recorded in notMaskRenderings with a reason. Adding a function to this
// package therefore cannot leave the net behind: the discovery set changes and
// this test goes red in the same edit.

// exportedStringFuncs binds every exported `func(string) string` in this
// package that renders or rewrites a value. TestExportedStringFuncs_AreAllBound
// fails when the package grows one that is missing here.
var exportedStringFuncs = map[string]func(string) string{
	"MaskValue":            MaskValue,
	"AuditMaskValue":       AuditMaskValue,
	"MaskDetectedSecrets":  MaskDetectedSecrets,
	"RedactSensitiveData":  RedactSensitiveData,
	"RedactURL":            RedactURL,
	"RedactURLQueryParams": RedactURLQueryParams,
	"ScrubUpstreamText":    ScrubUpstreamText,
	"ArgvFlagKey":          ArgvFlagKey,
	// Issue #1158, review round 2. Both emit masks, so the fail-closed net has
	// to know their markers even though no write door echoes them back.
	"LogSafeURL":           LogSafeURL,
	"LogSafeCallbackQuery": LogSafeCallbackQuery,
}

// Round 9 finding 5. Discovery covered PACKAGE-LEVEL functions only, and the
// renderings that actually reach the live read doors are METHODS on Redaction:
// `Leaf`, `EnvValue`, `HeaderValue`, `URLValue` and `Argv` are what
// RedactServerSecretFields, the MCP view and the generic walk all call, and
// they were hand-listed in maskRenderings with nothing enforcing registration
// — the exact hole round 8 closed for functions and left open for the half
// that matters.
//
// redactionMethodRenderers binds each one, under both policies and under both
// a sensitive and a benign key, so the property below probes the rendering a
// read door actually publishes.
var redactionMethodRenderers = map[string][]func(string) string{
	"Redaction.Leaf": {
		func(s string) string { return LiveRedaction.Leaf("token", s) },
		func(s string) string { return LiveRedaction.Leaf("benign", s) },
		func(s string) string { return LiveRedaction.Leaf("url", s) },
		func(s string) string { return AuditRedaction.Leaf("token", s) },
	},
	"Redaction.EnvValue": {
		func(s string) string { return LiveRedaction.EnvValue("API_KEY", s) },
		func(s string) string { return LiveRedaction.EnvValue("BENIGN", s) },
		func(s string) string { return AuditRedaction.EnvValue("API_KEY", s) },
	},
	"Redaction.HeaderValue": {
		func(s string) string { return LiveRedaction.HeaderValue("Authorization", s) },
		func(s string) string { return LiveRedaction.HeaderValue("X-Trace", s) },
		func(s string) string { return AuditRedaction.HeaderValue("Authorization", s) },
	},
	"Redaction.URLValue": {
		func(s string) string { return LiveRedaction.URLValue(s) },
		func(s string) string { return AuditRedaction.URLValue(s) },
	},
	"Redaction.Argv": {
		// A flag/value pair and a lone positional token: the two shapes the
		// argv rule renders differently.
		func(s string) string { return strings.Join(LiveRedaction.Argv([]string{"--token", s}), " ") },
		func(s string) string { return strings.Join(LiveRedaction.Argv([]string{s}), " ") },
		func(s string) string { return strings.Join(AuditRedaction.Argv([]string{"--token", s}), " ") },
	},
	// Issue #1158. The three log-only spawn/URL renderings. They are bound here
	// rather than exempted because they DO emit masks, so the fail-closed net
	// must know their markers even though no write door echoes them back.
	"Redaction.SpawnArgv": {
		func(s string) string { return strings.Join(AuditRedaction.SpawnArgv([]string{"--token", s}), " ") },
		func(s string) string { return strings.Join(AuditRedaction.SpawnArgv([]string{"-e", "K=" + s}), " ") },
		func(s string) string { return strings.Join(LiveRedaction.SpawnArgv([]string{"--token", s}), " ") },
	},
	"Redaction.SpawnCommandString": {
		func(s string) string { return AuditRedaction.SpawnCommandString("npx mcp --token " + s) },
		func(s string) string { return LiveRedaction.SpawnCommandString("npx mcp --token " + s) },
	},
	"Redaction.URLValueDeep": {
		func(s string) string { return AuditRedaction.URLValueDeep(s) },
		func(s string) string { return LiveRedaction.URLValueDeep(s) },
	},
	"Redaction.ExtraParamValue": {
		func(s string) string { return AuditRedaction.ExtraParamValue("resource", s) },
		func(s string) string { return AuditRedaction.ExtraParamValue("audience", s) },
		func(s string) string { return LiveRedaction.ExtraParamValue("resource", s) },
	},
}

// notMaskRenderings records the discovered renderers that are NOT mask
// renderings, with the reason. A row here is a deliberate decision, and a stale
// one (naming something that no longer exists) fails the test — the same rule
// the field decision table lives by.
var notMaskRenderings = map[string]string{
	"ExtractResourceMetadataURL": "parses a WWW-Authenticate header and returns the resource-metadata URL it advertises; " +
		"it extracts, it does not mask, and its output never reaches a write door as an echoed value",
	"StateFingerprint": "not a RENDERING of its input at all: it is a one-way truncated SHA-256 that keeps " +
		"no byte of the OAuth state nonce, and it exists only to correlate log LINES with each other. " +
		"No read door publishes it and no write door consumes it, so there is nothing for the " +
		"fail-closed net to recognise — binding it would put a per-value hash into MaskMarkers, which " +
		"is meaningless. (Issue #1158, review round 2.)",
	"Redaction.CapString": "a property of the DESTINATION, not a rule: it caps a leaf for a PERSISTED surface " +
		"(the activity store) and introduces no mask of its own. The live read policies set no limit precisely " +
		"so a masked value is never truncated below what the write path can recognise, which is why binding it " +
		"here would probe an identity function and prove nothing.",
}

// maskProbes are the value shapes a read door actually carries: bare secrets,
// keyed secrets, connection strings, URLs with sensitive and unrecognised
// parameters, a vendor-formatted token, and two ordinary values that must
// survive untouched.
var maskProbes = []string{
	"supersecretvalue",
	"abcd",
	"API_KEY=supersecretvalue",
	"Authorization: Bearer supersecretvalue",
	"postgres://u:supersecretvalue@host/db",
	"https://host/mcp?access_token=supersecretvalue",
	"https://host/mcp?opaque=ghp_1234567890abcdefghijABCDEFGHIJ123456",
	"AKIAIOSFODNN7EXAMPLE",
	"npx",
	"/opt/tools/mcp",
}

func TestExportedStringFuncs_AreAllBound(t *testing.T) {
	discovered := discoverExportedStringFuncs(t)
	require.NotEmpty(t, discovered, "the AST discovery found nothing — it has stopped testing anything")
	require.Contains(t, discovered, "Redaction.Leaf",
		"discovery no longer reaches the Redaction METHODS — the renderings the live read doors actually "+
			"publish. That is the half round 9 found unenforced; a discovery that cannot see them reads as "+
			"coverage without being any.")

	for _, name := range discovered {
		_, bound := exportedStringFuncs[name]
		if _, boundMethod := redactionMethodRenderers[name]; boundMethod {
			bound = true
		}
		_, exempt := notMaskRenderings[name]
		assert.True(t, bound || exempt,
			"oauth.%s is an exported func(string) string — the shape of a mask rendering — and is "+
				"registered nowhere. Bind it into exportedStringFuncs (so its output is probed against "+
				"the fail-closed net) or record it in notMaskRenderings with a reason. A rendering the "+
				"net does not know about is accepted on the write path and PERSISTED over the credential.",
			name)
	}

	known := map[string]bool{}
	for _, name := range discovered {
		known[name] = true
	}
	for name := range exportedStringFuncs {
		assert.True(t, known[name], "exportedStringFuncs binds oauth.%s, which no longer exists", name)
	}
	for name := range redactionMethodRenderers {
		assert.True(t, known[name], "redactionMethodRenderers binds oauth.%s, which no longer exists", name)
	}
	for name := range notMaskRenderings {
		assert.True(t, known[name], "notMaskRenderings exempts oauth.%s, which no longer exists", name)
	}
}

// And the property the binding exists for: whenever a bound renderer REWRITES a
// value, the fail-closed net must recognise the result. A renderer that emits a
// marker MaskMarkers does not carry is a mask a write door would accept.
func TestExportedStringFuncs_RenderOnlyRecognisedMasks(t *testing.T) {
	check := func(name, probe, got string) {
		if got == probe {
			return // pass-through: nothing was masked, nothing to recognise
		}
		assert.True(t, ContainsMaskMarker(got),
			"oauth.%s(%q) = %q, which carries no marker the fail-closed net recognises. "+
				"Register the rendering in maskRenderings so MaskMarkers is derived from it, "+
				"or the net will accept this string on a write and persist it over the credential.",
			name, probe, got)
	}
	for name, fn := range exportedStringFuncs {
		for _, probe := range maskProbes {
			check(name, probe, fn(probe))
		}
	}
	// The half that matters: the METHODS the live read doors publish through.
	for name, renderers := range redactionMethodRenderers {
		for _, render := range renderers {
			for _, probe := range maskProbes {
				check(name, probe, render(probe))
			}
		}
	}
}

// discoverExportedStringFuncs parses this package's own source and returns the
// names of every exported top-level `func(string) string`, PLUS every exported
// method on Redaction that renders a value — one `string` or `[]string` result
// (round 9 finding 5). Methods come back as `Redaction.<Name>` so the two
// namespaces cannot collide.
func discoverExportedStringFuncs(t *testing.T) []string {
	t.Helper()
	dir := packageDir(t)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !fn.Name.IsExported() {
					continue
				}
				if fn.Recv == nil {
					if isStringToString(fn.Type) {
						names = append(names, fn.Name.Name)
					}
					continue
				}
				if receiverTypeName(fn.Recv) == "Redaction" && rendersAValue(fn.Type) {
					names = append(names, "Redaction."+fn.Name.Name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

func isStringToString(ft *ast.FuncType) bool {
	if ft.Params == nil || ft.Results == nil {
		return false
	}
	if len(ft.Results.List) != 1 || !isStringIdent(ft.Results.List[0].Type) {
		return false
	}
	count := 0
	for _, field := range ft.Params.List {
		if !isStringIdent(field.Type) {
			return false
		}
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		count += n
	}
	return count == 1
}

// receiverTypeName returns the bare type name of a method receiver.
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// rendersAValue reports whether a method returns exactly one rendered value —
// a `string` or a `[]string`. That is the shape of every mask rendering on
// Redaction, and it excludes the ones that return a policy (WithLimit) or a
// generic tree (Value, RedactNested).
func rendersAValue(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	res := ft.Results.List[0].Type
	if isStringIdent(res) {
		return true
	}
	arr, ok := res.(*ast.ArrayType)
	return ok && arr.Len == nil && isStringIdent(arr.Elt)
}

func isStringIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "string"
}

// packageDir returns the directory this test file lives in.
func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed; the source-parsing guards cannot run")
	return filepath.Dir(file)
}

// The defect TestExportedStringFuncs_RenderOnlyRecognisedMasks found on its
// first run, pinned as its own regression.
//
// A URL is re-serialised through net/url after its components are masked, so
// every LIVE read door publishes the mask PERCENT-ENCODED. MaskMarkers carried
// only the decoded spelling, so ContainsMaskMarker — which is
// UnmaskLiveURL's and CheckServerWriteMasks' residual check — did not recognise
// a masked URL at all. A client echoing one back at a write door whose key-bound
// revert could not bind it (host changed, parameter count changed, the mask sits
// in the path) had the MASK persisted over the live credential: the #1142
// corruption, still open after seven rounds.
func TestEscapedURLMask_IsRefusedOnTheWritePath(t *testing.T) {
	const stored = "https://host/mcp?access_token=supersecretvalue"

	masked := LiveRedaction.URLValue(stored)
	require.NotEqual(t, stored, masked, "precondition: the read door masks this URL")
	require.NotContains(t, masked, "supersecretvalue")
	assert.True(t, ContainsMaskMarker(masked),
		"the fail-closed net must recognise the URL rendering its own read doors publish: %q", masked)

	// A caller that edits the host cannot have the stored secret restored, so
	// the mask must be refused rather than written through.
	moved := strings.Replace(masked, "https://host/", "https://attacker.example/", 1)
	_, err := UnmaskLiveURL(moved, stored)
	require.Error(t, err, "an unbindable masked URL was ACCEPTED and would be persisted over the credential")

	// And the generic residual net, which is what makes a future field fail closed.
	require.Error(t, CheckServerWriteMasks("server.", map[string]interface{}{"url": moved}))
}
