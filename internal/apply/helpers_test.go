package apply

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/receipt"
	"github.com/rbutera/kipple/internal/rules"
	"github.com/rbutera/kipple/internal/scan"
	"github.com/rbutera/kipple/internal/testutil"
)

// These tests are written against the macOS catalogue's fixtures
// (Library/Caches, Library/Logs, Xcode) and test mechanics, not catalogue
// contents, so they use that catalogue on every host. The Linux catalogue
// has its own tests (internal/rules, and the Linux scenario tests).
func TestMain(m *testing.M) {
	rules.UseCatalogueOf(rules.PlatformDarwin)
	os.Exit(testutil.GuardedMain(m))
}

var (
	now = time.Now()
	old = now.Add(-200 * 24 * time.Hour).Truncate(time.Second)
)

// fakeProcs is a procs.Inspector with fixed answers.
type fakeProcs struct {
	bundles map[string]bool
	names   map[string]bool
	cwds    []string
	err     error
}

func (f fakeProcs) RunningBundleIDs(context.Context) (map[string]bool, error) {
	return f.bundles, f.err
}

func (f fakeProcs) RunningNames(_ context.Context, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = f.names[n]
	}
	return out, f.err
}

func (f fakeProcs) ProcessCwds(context.Context, []string) ([]string, error) { return f.cwds, f.err }

// homeMover is the receipt mover of a run into this platform's home
// Trash.
var homeMover = map[bool]string{true: plan.MoverFreedesktop, false: plan.MoverRename}[runtime.GOOS == "linux"]

// fx is a fixture home with a Trash folder, a pinned environment and a
// receipts store.
type fx struct {
	t     *testing.T
	home  string
	env   policy.Env
	store receipt.Store
	// scanned is when the latest scan started.
	scanned time.Time
}

func newFx(t *testing.T, spec testutil.Spec) *fx {
	t.Helper()
	spec.Dirs = append(spec.Dirs, testutil.TrashDirs()...)
	home := testutil.NewHome(t, spec)
	fi, err := os.Lstat(home)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := policy.Identity(fi)
	store, err := receipt.Open(testutil.StateDir(home))
	if err != nil {
		t.Fatal(err)
	}
	return &fx{t: t, home: home, env: policy.Env{Home: home, HomeIdentity: &policy.FileIdentity{Dev: dev, Ino: ino}}, store: store}
}

func (f *fx) abs(rel string) string { return testutil.Abs(f.home, rel) }

func (f *fx) scan(level string) plan.Plan {
	f.t.Helper()
	f.scanned = time.Now()
	p, err := scan.Run(context.Background(), scan.Options{Env: f.env, Level: level, KippleVersion: "test", Now: func() time.Time { return now }})
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

// opts returns apply options for p with nothing running.
func (f *fx) opts(p plan.Plan) Options {
	return Options{
		Env:           f.env,
		Rules:         rules.Catalogue(),
		Plan:          p,
		Receipts:      f.store,
		KippleVersion: "test",
		Procs:         fakeProcs{},
		Now:           func() time.Time { return now },
		ChecksSince:   f.scanned,
	}
}

func (f *fx) run(o Options) plan.Result {
	f.t.Helper()
	res, err := Run(context.Background(), o)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

// roundTrip encodes and decodes p, as a plan file on disk would be.
func roundTrip(t *testing.T, p plan.Plan) plan.Plan {
	t.Helper()
	var buf bytes.Buffer
	if err := plan.Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	out, err := plan.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func itemByRel(t *testing.T, p plan.Plan, rel string) plan.Item {
	t.Helper()
	for _, it := range p.Items {
		if strings.TrimPrefix(it.Path, p.Home+"/") == rel {
			return it
		}
	}
	t.Fatalf("no item %s in the plan", rel)
	return plan.Item{}
}

func skipFor(res plan.Result, path string) (plan.SkippedItem, bool) {
	for _, s := range res.Skipped {
		if s.Path == path {
			return s, true
		}
	}
	return plan.SkippedItem{}, false
}

func movedFor(res plan.Result, path string) (plan.MovedItem, bool) {
	for _, m := range res.Moved {
		if m.Path == path {
			return m, true
		}
	}
	return plan.MovedItem{}, false
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func trashEntries(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, testutil.TrashRel))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// gitProject is a clean, pushed, idle repository with an ignored
// node_modules.
func gitProject(path string) testutil.Repo {
	return testutil.Repo{
		Path: path,
		Commits: []testutil.Commit{{Files: []testutil.File{
			{Path: "package.json", Content: "{}"},
			{Path: ".gitignore", Content: "node_modules/\n"},
			{Path: "src/index.js", Content: "x"},
		}}},
		Upstream: true,
		Pushed:   true,
		MTime:    old,
	}
}
