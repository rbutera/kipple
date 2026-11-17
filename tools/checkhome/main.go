// Command checkhome enforces kipple's "never touch the real home" rule at the
// source level. It is run by `make check-no-real-home` and in CI.
//
// It fails when:
//
//   - any Go file outside cmd/ (non-test files only) resolves the real home
//     directory: os.UserHomeDir, os/user.Current/Lookup/LookupId,
//     os.Getenv/os.LookupEnv/syscall.Getenv of "HOME" (a string literal, or
//     a constant expression from the same package that evaluates to "HOME"),
//     or os.ExpandEnv/os.Expand of a string mentioning $HOME or ${HOME}. The
//     single exception is internal/testutil/guard.go, which may use
//     os.UserHomeDir and os/user (and nothing else on this list) to refuse
//     the real home.
//   - any package with _test.go files is not guarded: it must either have a
//     TestMain that calls testutil.GuardedMain, or every Test/Benchmark/Fuzz
//     function must call testutil.GuardRealHome (and it may then have no
//     Example functions, which cannot call the guard). The call must resolve
//     to the internal/testutil package: through its import, a dot import, or
//     unqualified from inside package testutil itself.
//
// Usage: go run ./tools/checkhome [repo-root]
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// allowedHomeFile is the one library file permitted to read the real home,
// solely so the test guard can refuse it.
const allowedHomeFile = "internal/testutil/guard.go"

// testutilPath is the import path of the guard package.
const (
	testutilPath = "github.com/rbutera/kipple/internal/testutil"
	testutilDir  = "internal/testutil"
)

// homeVarRef matches $HOME or ${HOME} but not $HOMEBREW and the like.
var homeVarRef = regexp.MustCompile(`\$(\{HOME\}|HOME($|[^A-Za-z0-9_]))`)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	res, err := Check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check-no-real-home: error:", err)
		os.Exit(1)
	}
	for _, v := range res.Violations {
		fmt.Fprintln(os.Stderr, "check-no-real-home:", v)
	}
	if len(res.Violations) > 0 {
		fmt.Fprintf(os.Stderr, "check-no-real-home: FAILED with %d violation(s)\n", len(res.Violations))
		os.Exit(1)
	}
	fmt.Printf("check-no-real-home: OK (%d Go files, %d tested packages guarded)\n", res.Files, res.GuardedPackages)
}

// Result is the outcome of a Check run.
type Result struct {
	Files           int
	GuardedPackages int
	Violations      []string
}

type testPkg struct {
	hasGuardedMain bool
	hasTestMain    bool
	unguarded      []string // Test/Benchmark/Fuzz funcs lacking GuardRealHome
	examples       []string
}

type parsedFile struct {
	rel  string
	dir  string
	file *ast.File
	test bool
}

// Check walks root and returns every violation, sorted.
func Check(root string) (Result, error) {
	var res Result
	fset := token.NewFileSet()
	var files []parsedFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		files = append(files, parsedFile{rel: rel, dir: filepath.ToSlash(filepath.Dir(rel)), file: f, test: strings.HasSuffix(name, "_test.go")})
		return nil
	})
	if err != nil {
		return res, err
	}
	res.Files = len(files)

	// Constants are resolved per directory, so a const in one file of a
	// package is seen from the others.
	consts := map[string]map[string]ast.Expr{}
	for _, pf := range files {
		if consts[pf.dir] == nil {
			consts[pf.dir] = map[string]ast.Expr{}
		}
		collectConsts(pf.file, consts[pf.dir])
	}

	pkgs := map[string]*testPkg{}
	for _, pf := range files {
		cmdLib := !pf.test && strings.HasPrefix(pf.rel, "cmd/")
		if !cmdLib {
			for _, v := range homeReads(fset, pf.file, consts[pf.dir]) {
				if pf.rel == allowedHomeFile && v.guardOK {
					continue
				}
				res.Violations = append(res.Violations, fmt.Sprintf("%s:%d: %s outside cmd/ (only cmd/ may resolve the real home; inject it instead)", pf.rel, v.line, v.what))
			}
		}
		if pf.test {
			p := pkgs[pf.dir]
			if p == nil {
				p = &testPkg{}
				pkgs[pf.dir] = p
			}
			scanTestFile(pf.file, pf.dir, p)
		}
	}

	dirs := make([]string, 0, len(pkgs))
	for dir := range pkgs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		p := pkgs[dir]
		switch {
		case p.hasGuardedMain:
			res.GuardedPackages++
		case p.hasTestMain:
			res.Violations = append(res.Violations, fmt.Sprintf("%s: TestMain does not call testutil.GuardedMain", dir))
		case len(p.examples) > 0:
			res.Violations = append(res.Violations, fmt.Sprintf("%s: has Example functions (%s) but no TestMain calling testutil.GuardedMain", dir, strings.Join(p.examples, ", ")))
		case len(p.unguarded) > 0:
			res.Violations = append(res.Violations, fmt.Sprintf("%s: no TestMain calling testutil.GuardedMain and these tests do not call testutil.GuardRealHome: %s", dir, strings.Join(p.unguarded, ", ")))
		default:
			res.GuardedPackages++
		}
	}
	sort.Strings(res.Violations)
	return res, nil
}

// collectConsts records every constant declared with an explicit value in f,
// at package level or inside functions (an over-approximation: a shadowing
// variable of the same name is treated as the constant).
func collectConsts(f *ast.File, into map[string]ast.Expr) {
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				into[name.Name] = vs.Values[i]
			}
		}
		return true
	})
}

// constString evaluates a string constant expression: literals, parentheses,
// concatenation and identifiers naming collected constants.
func constString(e ast.Expr, consts map[string]ast.Expr, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.ParenExpr:
		return constString(x.X, consts, depth+1)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := constString(x.X, consts, depth+1)
		b, ok2 := constString(x.Y, consts, depth+1)
		return a + b, ok1 && ok2
	case *ast.Ident:
		if v, ok := consts[x.Name]; ok {
			return constString(v, consts, depth+1)
		}
	}
	return "", false
}

type homeRead struct {
	line    int
	what    string
	guardOK bool // permitted in allowedHomeFile
}

// importNames returns the local names a file uses for an import path, and
// whether it is dot-imported.
func importNames(f *ast.File, path string, def string) (map[string]bool, bool) {
	names := map[string]bool{}
	dot := false
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p != path {
			continue
		}
		switch {
		case imp.Name == nil:
			names[def] = true
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name == "_":
		default:
			names[imp.Name.Name] = true
		}
	}
	return names, dot
}

// homeReads finds references that resolve the real home directory.
func homeReads(fset *token.FileSet, f *ast.File, consts map[string]ast.Expr) []homeRead {
	osNames, osDot := importNames(f, "os", "os")
	userNames, userDot := importNames(f, "os/user", "user")
	sysNames, sysDot := importNames(f, "syscall", "syscall")

	var out []homeRead
	add := func(n ast.Node, what string, guardOK bool) {
		out = append(out, homeRead{line: fset.Position(n.Pos()).Line, what: what, guardOK: guardOK})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			if osNames[id.Name] && x.Sel.Name == "UserHomeDir" {
				add(x, "os.UserHomeDir", true)
			}
			if userNames[id.Name] && (x.Sel.Name == "Current" || x.Sel.Name == "Lookup" || x.Sel.Name == "LookupId") {
				add(x, "os/user."+x.Sel.Name, true)
			}
		case *ast.Ident:
			if osDot && x.Name == "UserHomeDir" {
				add(x, "os.UserHomeDir (dot import)", true)
			}
			if userDot && (x.Name == "Current" || x.Name == "Lookup" || x.Name == "LookupId") {
				add(x, "os/user."+x.Name+" (dot import)", true)
			}
		case *ast.CallExpr:
			pkg, fn := "", ""
			switch f := x.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok {
					switch {
					case osNames[id.Name]:
						pkg, fn = "os", f.Sel.Name
					case sysNames[id.Name]:
						pkg, fn = "syscall", f.Sel.Name
					}
				}
			case *ast.Ident:
				// A dot import of both os and syscall is ambiguous to the
				// compiler anyway; report it as os.
				if osDot {
					pkg, fn = "os", f.Name
				} else if sysDot {
					pkg, fn = "syscall", f.Name
				}
			}
			if len(x.Args) == 0 {
				return true
			}
			switch {
			case (pkg == "os" && (fn == "Getenv" || fn == "LookupEnv")) || (pkg == "syscall" && fn == "Getenv"):
				if v, ok := constString(x.Args[0], consts, 0); ok && v == "HOME" {
					add(x, pkg+"."+fn+`("HOME")`, false)
				}
			case pkg == "os" && (fn == "ExpandEnv" || fn == "Expand"):
				if v, ok := constString(x.Args[0], consts, 0); ok && homeVarRef.MatchString(v) {
					add(x, "os."+fn+" of $HOME", false)
				}
			}
		}
		return true
	})
	return out
}

func scanTestFile(f *ast.File, dir string, p *testPkg) {
	quals, dot := importNames(f, testutilPath, "testutil")
	// Unqualified calls resolve to testutil only via a dot import or from
	// inside package testutil itself.
	unqualified := dot || (dir == testutilDir && f.Name.Name == "testutil")
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Body == nil {
			continue
		}
		name := fd.Name.Name
		switch {
		case name == "TestMain":
			p.hasTestMain = true
			if callsGuard(fd.Body, "GuardedMain", quals, unqualified) {
				p.hasGuardedMain = true
			}
		case isTestFunc(name, "Test"), isTestFunc(name, "Benchmark"), isTestFunc(name, "Fuzz"):
			if !callsGuard(fd.Body, "GuardRealHome", quals, unqualified) {
				p.unguarded = append(p.unguarded, name)
			}
		case isTestFunc(name, "Example"):
			p.examples = append(p.examples, name)
		}
	}
}

// isTestFunc mirrors go test's naming rule: prefix, then end or a non-lowercase rune.
func isTestFunc(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	return rest == "" || rest[0] < 'a' || rest[0] > 'z'
}

// callsGuard reports whether body calls the testutil function name: as
// <q>.name where q is a local name of the testutil import, or unqualified
// when allowed.
func callsGuard(body *ast.BlockStmt, name string, quals map[string]bool, unqualified bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			found = unqualified && fn.Name == name
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok {
				found = quals[id.Name] && fn.Sel.Name == name
			}
		}
		return !found
	})
	return found
}
