package portability

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

const module = "github.com/rbutera/kipple"

// darwinUnix are golang.org/x/sys/unix symbols that exist only on macOS.
var darwinUnix = map[string]bool{
	"RenameatxNp": true, "RenamexNp": true, "RENAME_EXCL": true, "RENAME_SWAP": true,
	"F_GETPATH": true, "FcntlInt": true,
}

type goFile struct {
	path    string
	dir     string // module-relative package directory
	ast     *ast.File
	onLinux bool // built for GOOS=linux
	darwin  bool // built for GOOS=darwin
}

// builtFor reports whether a file is part of a build for goos, from its
// file-name suffix and //go:build line. cgo is assumed available.
func builtFor(name string, f *ast.File, goos string) bool {
	base := strings.TrimSuffix(filepath.Base(name), ".go")
	for _, os := range []string{"darwin", "linux", "windows", "freebsd", "openbsd", "netbsd"} {
		if strings.HasSuffix(base, "_"+os) && os != goos {
			return false
		}
	}
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return true
			}
			return expr.Eval(func(tag string) bool {
				switch tag {
				case goos, "unix", "cgo", "gc":
					return true
				}
				return strings.HasPrefix(tag, "go1.")
			})
		}
	}
	return true
}

func load(t *testing.T) []goFile {
	t.Helper()
	// The working directory may be reached through a symlink, and WalkDir
	// does not descend into a symlinked root.
	root, err := filepath.Abs("../..")
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	var files []goFile
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && p != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		files = append(files, goFile{path: p, dir: filepath.ToSlash(rel), ast: f, onLinux: builtFor(p, f, "linux"), darwin: builtFor(p, f, "darwin")})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func topLevel(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// Task 3.9: no darwin-only symbol is referenced from a file that is built
// on other platforms (any file without a darwin-only build constraint).
func TestNoDarwinOnlySymbolOutsideDarwinFiles(t *testing.T) {
	files := load(t)
	// darwinOnly[dir] = top-level names declared for darwin and not for linux.
	darwinOnly := map[string]map[string]bool{}
	onLinux := map[string]map[string]bool{}
	for _, f := range files {
		for _, n := range topLevel(f.ast) {
			if f.onLinux {
				if onLinux[f.dir] == nil {
					onLinux[f.dir] = map[string]bool{}
				}
				onLinux[f.dir][n] = true
			} else if f.darwin {
				if darwinOnly[f.dir] == nil {
					darwinOnly[f.dir] = map[string]bool{}
				}
				darwinOnly[f.dir][n] = true
			}
		}
	}
	for dir, names := range darwinOnly {
		for n := range names {
			if onLinux[dir][n] {
				delete(names, n)
			}
		}
	}
	// The seams of design D17 must exist: darwin-only code behind them.
	for dir, sym := range map[string]string{"internal/procs": "System", "internal/trash": "FinderTrasher"} {
		if !darwinOnly[dir][sym] {
			t.Errorf("%s.%s should be declared only in darwin files", dir, sym)
		}
	}

	var bad []string
	for _, f := range files {
		if !f.onLinux {
			continue
		}
		imports := map[string]string{} // local name -> import path
		for _, imp := range f.ast.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = p
		}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				x, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				p := imports[x.Name]
				switch {
				case p == "golang.org/x/sys/unix" && darwinUnix[n.Sel.Name]:
					bad = append(bad, f.path+": unix."+n.Sel.Name)
				case strings.HasPrefix(p, module+"/") && darwinOnly[strings.TrimPrefix(p, module+"/")][n.Sel.Name]:
					bad = append(bad, f.path+": "+x.Name+"."+n.Sel.Name)
				}
			case *ast.Ident:
				if darwinOnly[f.dir][n.Name] && n.Obj == nil {
					bad = append(bad, f.path+": "+n.Name)
				}
			}
			return true
		})
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("darwin-only symbol used in a file built on other platforms: %s", b)
	}
}

// The checker itself: a file that is built everywhere and calls a
// darwin-only function is caught.
func TestCheckerCatchesAViolation(t *testing.T) {
	src := "package x\n\nimport \"golang.org/x/sys/unix\"\n\nfunc f() { _ = unix.RenameatxNp }\n"
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if !builtFor("x.go", f, "linux") {
		t.Fatal("an unconstrained file must count as built on linux")
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && darwinUnix[s.Sel.Name] {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("violation not seen")
	}
	g, _ := parser.ParseFile(token.NewFileSet(), "y_darwin.go", "//go:build darwin\n\npackage x\n", parser.ParseComments)
	if builtFor("y_darwin.go", g, "linux") {
		t.Fatal("a darwin file must not count as built on linux")
	}
}
