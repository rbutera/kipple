package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

const guardedTest = `package p
import ("os"; "testing"; "github.com/rbutera/kipple/internal/testutil")
func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }
func TestX(t *testing.T) {}
`

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string // substrings, one per expected violation; empty means clean
	}{
		{
			name: "clean tree passes",
			files: map[string]string{
				"internal/a/a.go":      "package a\nfunc F(home string) string { return home }\n",
				"internal/a/a_test.go": guardedTest,
			},
		},
		{
			name: "UserHomeDir in library fails",
			files: map[string]string{
				"internal/a/a.go": "package a\nimport \"os\"\nfunc F() { _, _ = os.UserHomeDir() }\n",
			},
			want: []string{"internal/a/a.go:3: os.UserHomeDir"},
		},
		{
			name: "aliased os import still caught",
			files: map[string]string{
				"internal/a/a.go": "package a\nimport xos \"os\"\nvar f = xos.UserHomeDir\n",
			},
			want: []string{"os.UserHomeDir"},
		},
		{
			name: "dot import still caught",
			files: map[string]string{
				"internal/a/a.go": "package a\nimport . \"os\"\nfunc F() { _, _ = UserHomeDir() }\n",
			},
			want: []string{"dot import"},
		},
		{
			name: "Getenv HOME fails",
			files: map[string]string{
				"internal/a/a.go": "package a\nimport \"os\"\nfunc F() string { return os.Getenv(\"HOME\") }\n",
			},
			want: []string{`os.Getenv("HOME")`},
		},
		{
			name: "os/user.Current fails",
			files: map[string]string{
				"internal/a/a.go": "package a\nimport \"os/user\"\nfunc F() { _, _ = user.Current() }\n",
			},
			want: []string{"os/user.Current"},
		},
		{
			name: "string mention is not a violation",
			files: map[string]string{
				"internal/a/a.go": "package a\n// os.UserHomeDir is forbidden here.\nconst s = \"os.UserHomeDir\"\n",
			},
		},
		{
			name: "cmd may resolve home",
			files: map[string]string{
				"cmd/kipple/main.go": "package main\nimport \"os\"\nfunc main() { _, _ = os.UserHomeDir() }\n",
			},
		},
		{
			name: "cmd tests may not resolve home",
			files: map[string]string{
				"cmd/kipple/main_test.go": "package main\nimport (\"os\"; \"testing\"; \"github.com/rbutera/kipple/internal/testutil\")\nfunc TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }\nfunc TestX(t *testing.T) { _, _ = os.UserHomeDir() }\n",
			},
			want: []string{"cmd/kipple/main_test.go:4: os.UserHomeDir"},
		},
		{
			name: "testutil guard file is the single exception",
			files: map[string]string{
				"internal/testutil/guard.go": "package testutil\nimport \"os\"\nfunc r() (string, error) { return os.UserHomeDir() }\n",
				"internal/testutil/other.go": "package testutil\nimport \"os\"\nfunc s() (string, error) { return os.UserHomeDir() }\n",
			},
			want: []string{"internal/testutil/other.go:3"},
		},
		{
			name: "test package without guard fails",
			files: map[string]string{
				"internal/a/a_test.go": "package a\nimport \"testing\"\nfunc TestX(t *testing.T) {}\nfunc TestY(t *testing.T) {}\n",
			},
			want: []string{"internal/a: no TestMain calling testutil.GuardedMain and these tests do not call testutil.GuardRealHome: TestX, TestY"},
		},
		{
			name: "per-test guard calls are accepted",
			files: map[string]string{
				"internal/a/a_test.go": "package a\nimport (\"testing\"; \"github.com/rbutera/kipple/internal/testutil\")\nfunc TestX(t *testing.T) { testutil.GuardRealHome(t) }\n",
			},
		},
		{
			name: "one unguarded test among guarded ones fails",
			files: map[string]string{
				"internal/a/a_test.go": "package a\nimport (\"testing\"; \"github.com/rbutera/kipple/internal/testutil\")\nfunc TestX(t *testing.T) { testutil.GuardRealHome(t) }\nfunc TestY(t *testing.T) {}\n",
			},
			want: []string{"do not call testutil.GuardRealHome: TestY"},
		},
		{
			name: "TestMain without GuardedMain fails",
			files: map[string]string{
				"internal/a/a_test.go": "package a\nimport (\"os\"; \"testing\")\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n",
			},
			want: []string{"internal/a: TestMain does not call testutil.GuardedMain"},
		},
		{
			name: "external test package in same dir shares the TestMain",
			files: map[string]string{
				"internal/a/a_test.go":   guardedTest,
				"internal/a/ext_test.go": "package p_test\nimport \"testing\"\nfunc TestZ(t *testing.T) {}\n",
			},
		},
		{
			name: "example functions need TestMain",
			files: map[string]string{
				"internal/a/a_test.go": "package a\nfunc ExampleF() {}\n",
			},
			want: []string{"has Example functions (ExampleF)"},
		},
		{
			name: "testdata is ignored",
			files: map[string]string{
				"internal/a/testdata/x.go": "package x\nimport \"os\"\nfunc F() { _, _ = os.UserHomeDir() }\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTree(t, tc.files)
			res, err := Check(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Violations) != len(tc.want) {
				t.Fatalf("got %d violations, want %d:\n%s", len(res.Violations), len(tc.want), strings.Join(res.Violations, "\n"))
			}
			for i, w := range tc.want {
				if !strings.Contains(res.Violations[i], w) {
					t.Errorf("violation %d = %q, want it to contain %q", i, res.Violations[i], w)
				}
			}
		})
	}
}

// TestRepoIsClean runs the checker over this repository, so `go test` alone
// catches a violation even without `make check-no-real-home`.
func TestRepoIsClean(t *testing.T) {
	res, err := Check(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) > 0 {
		t.Fatalf("repository violates the no-real-home rule:\n%s", strings.Join(res.Violations, "\n"))
	}
}
