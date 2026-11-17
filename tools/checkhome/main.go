// Command checkhome enforces kipple's "never touch the real home" rule at the
// source level. It is run by `make check-no-real-home` and in CI.
//
// It fails when:
//
//   - any Go file outside cmd/ (non-test files only) resolves the real home
//     directory: os.UserHomeDir, os/user.Current/Lookup/LookupId, or
//     os.Getenv/os.LookupEnv("HOME"). The single exception is
//     internal/testutil/guard.go, which needs the real home to refuse it.
//   - any package with _test.go files is not guarded: it must either have a
//     TestMain that calls testutil.GuardedMain, or every Test/Benchmark/Fuzz
//     function must call testutil.GuardRealHome (and it may then have no
//     Example functions, which cannot call the guard).
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
	"sort"
	"strconv"
	"strings"
)

// allowedHomeFile is the one library file permitted to read the real home,
// solely so the test guard can refuse it.
const allowedHomeFile = "internal/testutil/guard.go"

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

// Check walks root and returns every violation, sorted.
func Check(root string) (Result, error) {
	var res Result
	pkgs := map[string]*testPkg{}
	fset := token.NewFileSet()

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
		res.Files++
		isTest := strings.HasSuffix(name, "_test.go")

		exempt := rel == allowedHomeFile || (!isTest && strings.HasPrefix(rel, "cmd/"))
		if !exempt {
			for _, v := range homeReads(fset, f) {
				res.Violations = append(res.Violations, fmt.Sprintf("%s:%d: %s outside cmd/ (only cmd/ may resolve the real home; inject it instead)", rel, v.line, v.what))
			}
		}

		if isTest {
			dir := filepath.ToSlash(filepath.Dir(rel))
			p := pkgs[dir]
			if p == nil {
				p = &testPkg{}
				pkgs[dir] = p
			}
			scanTestFile(f, p)
		}
		return nil
	})
	if err != nil {
		return res, err
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

type homeRead struct {
	line int
	what string
}

// homeReads finds references that resolve the real home directory.
func homeReads(fset *token.FileSet, f *ast.File) []homeRead {
	osNames := map[string]bool{}
	userNames := map[string]bool{}
	osDot, userDot := false, false
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		local := ""
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch path {
		case "os":
			switch local {
			case "":
				osNames["os"] = true
			case ".":
				osDot = true
			case "_":
			default:
				osNames[local] = true
			}
		case "os/user":
			switch local {
			case "":
				userNames["user"] = true
			case ".":
				userDot = true
			case "_":
			default:
				userNames[local] = true
			}
		}
	}

	var out []homeRead
	add := func(n ast.Node, what string) {
		out = append(out, homeRead{line: fset.Position(n.Pos()).Line, what: what})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			if osNames[id.Name] && x.Sel.Name == "UserHomeDir" {
				add(x, "os.UserHomeDir")
			}
			if userNames[id.Name] && (x.Sel.Name == "Current" || x.Sel.Name == "Lookup" || x.Sel.Name == "LookupId") {
				add(x, "os/user."+x.Sel.Name)
			}
		case *ast.Ident:
			if osDot && x.Name == "UserHomeDir" {
				add(x, "os.UserHomeDir (dot import)")
			}
			if userDot && (x.Name == "Current" || x.Name == "Lookup" || x.Name == "LookupId") {
				add(x, "os/user."+x.Name+" (dot import)")
			}
		case *ast.CallExpr:
			fn := ""
			switch f := x.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok && osNames[id.Name] {
					fn = f.Sel.Name
				}
			case *ast.Ident:
				if osDot {
					fn = f.Name
				}
			}
			if (fn == "Getenv" || fn == "LookupEnv") && len(x.Args) == 1 {
				if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil && v == "HOME" {
						add(x, "os."+fn+`("HOME")`)
					}
				}
			}
		}
		return true
	})
	return out
}

func scanTestFile(f *ast.File, p *testPkg) {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Body == nil {
			continue
		}
		name := fd.Name.Name
		switch {
		case name == "TestMain":
			p.hasTestMain = true
			if callsNamed(fd.Body, "GuardedMain") {
				p.hasGuardedMain = true
			}
		case isTestFunc(name, "Test"), isTestFunc(name, "Benchmark"), isTestFunc(name, "Fuzz"):
			if !callsNamed(fd.Body, "GuardRealHome") {
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

// callsNamed reports whether body contains a call to name or <pkg>.name.
func callsNamed(body *ast.BlockStmt, name string) bool {
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
			found = fn.Name == name
		case *ast.SelectorExpr:
			found = fn.Sel.Name == name
		}
		return !found
	})
	return found
}
