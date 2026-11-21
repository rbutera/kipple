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

// platformUnix are golang.org/x/sys/unix symbols that exist only on one of
// the two supported platforms: a file built for the other one must never
// refer to them.
var platformUnix = map[string]map[string]bool{
	"darwin": {
		"RenameatxNp": true, "RenamexNp": true, "RENAME_EXCL": true, "RENAME_SWAP": true,
		"F_GETPATH": true, "FcntlInt": true, "ENOATTR": true,
	},
	"linux": {
		"Renameat2": true, "RENAME_NOREPLACE": true, "RENAME_EXCHANGE": true,
		"Statx": true, "Statx_t": true, "STATX_ATTR_MOUNT_ROOT": true, "STATX_BASIC_STATS": true,
		"AT_EMPTY_PATH": true, "O_PATH": true, "Openat2": true, "OpenHow": true,
	},
}

// platforms are the two operating systems kipple supports; each is checked
// against the other.
var platforms = []string{"darwin", "linux"}

type goFile struct {
	path string
	dir  string // module-relative package directory
	ast  *ast.File
	on   map[string]bool // built for GOOS=darwin, GOOS=linux
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
	return loadTree(t, root)
}

// loadTree parses every non-test Go file under root.
func loadTree(t *testing.T, root string) []goFile {
	t.Helper()
	var files []goFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		on := map[string]bool{}
		for _, goos := range platforms {
			on[goos] = builtFor(p, f, goos)
		}
		files = append(files, goFile{path: p, dir: filepath.ToSlash(rel), ast: f, on: on})
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

// onlyOn returns, per package directory, the top-level names declared in
// files built for goos and in no file built for the other platform.
func onlyOn(files []goFile, goos, other string) map[string]map[string]bool {
	only := map[string]map[string]bool{}
	elsewhere := map[string]map[string]bool{}
	add := func(m map[string]map[string]bool, dir, n string) {
		if m[dir] == nil {
			m[dir] = map[string]bool{}
		}
		m[dir][n] = true
	}
	for _, f := range files {
		for _, n := range topLevel(f.ast) {
			switch {
			case f.on[other]:
				add(elsewhere, f.dir, n)
			case f.on[goos]:
				add(only, f.dir, n)
			}
		}
	}
	for dir, names := range only {
		for n := range names {
			if elsewhere[dir][n] {
				delete(names, n)
			}
		}
	}
	return only
}

// violations lists every reference, in a file built for one supported
// platform, to a symbol that exists only on the other one: a
// golang.org/x/sys/unix symbol of platformUnix, or a top-level name of this
// module declared only in the other platform's files.
func violations(files []goFile) []string {
	var bad []string
	for _, goos := range platforms {
		other := "linux"
		if goos == "linux" {
			other = "darwin"
		}
		foreign := onlyOn(files, other, goos)
		for _, f := range files {
			if !f.on[goos] {
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
					case p == "golang.org/x/sys/unix" && platformUnix[other][n.Sel.Name]:
						bad = append(bad, f.path+": "+other+"-only unix."+n.Sel.Name)
					case strings.HasPrefix(p, module+"/") && foreign[strings.TrimPrefix(p, module+"/")][n.Sel.Name]:
						bad = append(bad, f.path+": "+other+"-only "+x.Name+"."+n.Sel.Name)
					}
				case *ast.Ident:
					if foreign[f.dir][n.Name] && n.Obj == nil {
						bad = append(bad, f.path+": "+other+"-only "+n.Name)
					}
				}
				return true
			})
		}
	}
	sort.Strings(bad)
	return bad
}

// Task 3.9 of kipple-v1 and task 1.4 of linux-port: no darwin-only symbol is
// referenced from a file that is built on Linux, and no Linux-only symbol
// from a file that is built on macOS.
func TestNoPlatformOnlySymbolOutsideItsFiles(t *testing.T) {
	files := load(t)
	// The seams of design D17 must exist: platform code behind them.
	darwinOnly := onlyOn(files, "darwin", "linux")
	for dir, sym := range map[string]string{"internal/procs": "System", "internal/trash": "FinderTrasher"} {
		if !darwinOnly[dir][sym] {
			t.Errorf("%s.%s should be declared only in darwin files", dir, sym)
		}
	}
	linuxOnly := onlyOn(files, "linux", "darwin")
	for dir, sym := range map[string]string{"internal/procs": "procDir", "internal/trash": "FreedesktopTrasher", "internal/policy": "procSelfFd"} {
		if !linuxOnly[dir][sym] {
			t.Errorf("%s.%s should be declared only in linux files", dir, sym)
		}
	}
	for _, b := range violations(files) {
		t.Errorf("platform-only symbol used in a file built on the other platform: %s", b)
	}
}

// A Linux-only declaration that a file built everywhere refers to is
// caught (the "misplaced Linux-only symbol" check of task 1.4), as is a
// Linux-only x/sys/unix symbol in a darwin file.
func TestCheckerCatchesAMisplacedLinuxSymbol(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("x/a_linux.go", "package x\n\nfunc onlyLinux() {}\n")
	write("x/b.go", "package x\n\nfunc everywhere() { onlyLinux() }\n")
	write("x/c_darwin.go", "package x\n\nimport \"golang.org/x/sys/unix\"\n\nvar _ = unix.Renameat2\n")
	bad := violations(loadTree(t, root))
	want := []string{"b.go: linux-only onlyLinux", "c_darwin.go: linux-only unix.Renameat2"}
	for _, w := range want {
		found := false
		for _, b := range bad {
			found = found || strings.HasSuffix(b, w)
		}
		if !found {
			t.Errorf("violation %q not reported; got %q", w, bad)
		}
	}
	if len(bad) != len(want) {
		t.Errorf("got %d violations, want %d: %q", len(bad), len(want), bad)
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
		if s, ok := n.(*ast.SelectorExpr); ok && platformUnix["darwin"][s.Sel.Name] {
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
