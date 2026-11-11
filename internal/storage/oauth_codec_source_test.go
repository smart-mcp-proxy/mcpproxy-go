package storage

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Spec 123 T015 (FR-010, FR-017): source tests for the codec seam and the
// storage open sites. They use the standard library only (go/parser +
// go/types with the source importer).

const storageImportPath = "github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

// codecFuncs are the only functions allowed to marshal or unmarshal an
// OAuthTokenRecord.
var codecFuncs = map[string]bool{
	"encodeOAuthRecord": true,
	"decodeOAuthRecord": true,
}

// oauthBucketAccessExempt are the only functions allowed to call Get, Put,
// ForEach or Cursor on the oauth_tokens bucket: the write seam, the
// single-row read, the key-only clear and the listing helper. Everything
// else goes through them.
var oauthBucketAccessExempt = map[string]string{
	"putOAuthRecord":    "write seam (encodes through encodeOAuthRecord)",
	"getOAuthRecordTx":  "single-row read (decodes through decodeOAuthRecord)",
	"clearOAuthStateTx": "key-only delete of a server's rows",
	"collectOAuthRows":  "listing helper (copies key/value pairs for the codec)",
}

type storagePkg struct {
	fset  *token.FileSet
	files []*ast.File
	info  *types.Info
	pkg   *types.Package
}

func loadStoragePkg(t *testing.T) *storagePkg {
	t.Helper()
	if testing.Short() {
		t.Skip("type-checks internal/storage from source")
	}
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(storageImportPath, fset, files, info)
	if err != nil {
		t.Fatalf("type-check internal/storage: %v", err)
	}
	return &storagePkg{fset: fset, files: files, info: info, pkg: pkg}
}

// funcName names a declaration as Recv.Name or Name.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	rt := fd.Recv.List[0].Type
	if s, ok := rt.(*ast.StarExpr); ok {
		rt = s.X
	}
	if id, ok := rt.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func isNamed(t types.Type, pkgPath, name string) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Name() == name && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkgPath
}

func TestOAuthBucketValuesOnlyThroughCodec(t *testing.T) {
	p := loadStoragePkg(t)
	bucketConst := p.pkg.Scope().Lookup("OAuthTokenBucket")
	if bucketConst == nil {
		t.Fatal("OAuthTokenBucket not found")
	}
	const bboltPath = "go.etcd.io/bbolt"

	// isOAuthBucketExpr: tx.Bucket(...OAuthTokenBucket...).
	isOAuthBucketExpr := func(e ast.Expr) bool {
		call, ok := ast.Unparen(e).(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Bucket" {
			return false
		}
		found := false
		for _, a := range call.Args {
			ast.Inspect(a, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && p.info.Uses[id] == bucketConst {
					found = true
				}
				return !found
			})
		}
		return found
	}
	tainted := map[types.Object]bool{}
	isTainted := func(e ast.Expr) bool {
		e = ast.Unparen(e)
		if id, ok := e.(*ast.Ident); ok {
			return tainted[p.info.Uses[id]]
		}
		return isOAuthBucketExpr(e)
	}
	// Propagate "is the oauth_tokens bucket" through assignments and into
	// the parameters of package functions it is passed to, to a fixpoint.
	for changed := true; changed; {
		changed = false
		taint := func(o types.Object) {
			if o != nil && !tainted[o] {
				tainted[o] = true
				changed = true
			}
		}
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					if len(n.Lhs) == len(n.Rhs) {
						for i, r := range n.Rhs {
							if id, ok := n.Lhs[i].(*ast.Ident); ok && isTainted(r) {
								if o := p.info.Defs[id]; o != nil {
									taint(o)
								} else {
									taint(p.info.Uses[id])
								}
							}
						}
					}
				case *ast.ValueSpec:
					if len(n.Names) == len(n.Values) {
						for i, v := range n.Values {
							if isTainted(v) {
								taint(p.info.Defs[n.Names[i]])
							}
						}
					}
				case *ast.CallExpr:
					var fn *types.Func
					switch f := n.Fun.(type) {
					case *ast.Ident:
						fn, _ = p.info.Uses[f].(*types.Func)
					case *ast.SelectorExpr:
						fn, _ = p.info.Uses[f.Sel].(*types.Func)
					}
					if fn == nil || fn.Pkg() != p.pkg {
						return true
					}
					sig := fn.Type().(*types.Signature)
					for i, a := range n.Args {
						if i < sig.Params().Len() && isTainted(a) {
							taint(sig.Params().At(i))
						}
					}
				}
				return true
			})
		}
	}

	var violations []string
	for _, f := range p.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := funcName(fd)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos := p.fset.Position(sel.Pos())
				where := filepath.Base(pos.Filename) + ":" + strconv.Itoa(pos.Line) + " in " + name
				switch sel.Sel.Name {
				case "MarshalBinary", "UnmarshalBinary":
					if s := p.info.Selections[sel]; s != nil && isNamed(s.Recv(), storageImportPath, "OAuthTokenRecord") && !codecFuncs[fd.Name.Name] {
						violations = append(violations, "OAuthTokenRecord."+sel.Sel.Name+" outside the codec: "+where)
					}
				case "Get", "Put", "ForEach", "Cursor":
					s := p.info.Selections[sel]
					if s == nil || !isNamed(s.Recv(), bboltPath, "Bucket") || !isTainted(sel.X) {
						return true
					}
					if _, ok := oauthBucketAccessExempt[fd.Name.Name]; !ok {
						violations = append(violations, "oauth_tokens bucket ."+sel.Sel.Name+" outside the codec: "+where)
					}
				}
				return true
			})
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestStorageOpenSitesPassOAuthKey (FR-017): every non-test
// storage.NewManager / storage.NewBoltDB call in the module passes
// storage.WithOAuthTokenKey, so no open site can write plaintext while a key
// is configured.
func TestStorageOpenSitesPassOAuthKey(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	skipDirs := map[string]bool{"node_modules": true, "vendor": true, "testdata": true, "frontend": true, "website": true, "native": true}
	var sites, violations []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		local := ""
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == storageImportPath {
				local = "storage"
				if imp.Name != nil {
					local = imp.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		isPkgSel := func(e ast.Expr, names ...string) bool {
			sel, ok := e.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != local {
				return false
			}
			for _, n := range names {
				if sel.Sel.Name == n {
					return true
				}
			}
			return false
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isPkgSel(call.Fun, "NewManager", "NewBoltDB") {
				return true
			}
			rel, _ := filepath.Rel(root, path)
			where := rel + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
			sites = append(sites, where)
			for _, a := range call.Args {
				if c, ok := a.(*ast.CallExpr); ok && isPkgSel(c.Fun, "WithOAuthTokenKey") {
					return true
				}
			}
			violations = append(violations, where)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) == 0 {
		t.Fatal("found no storage open site: the walk is broken")
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("storage open site without storage.WithOAuthTokenKey: %s", v)
	}
}
