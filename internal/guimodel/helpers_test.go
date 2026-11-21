package guimodel

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/gitstate"
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

var old = time.Now().Add(-200 * 24 * time.Hour)

// idle is a procs.Inspector with nothing running.
type idle struct{}

func (idle) RunningBundleIDs(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
func (idle) RunningNames(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (idle) ProcessCwds(context.Context, []string) ([]string, error) { return nil, nil }

// fixtureSpec has a safe cache, a log, a review Go module cache, a manual
// installer, an info Ollama model and an idle project.
func fixtureSpec() testutil.Spec {
	return testutil.Spec{
		Files: []testutil.File{
			{Path: "Library/Caches/com.example.app/blob", Size: 64 << 10, MTime: old},
			{Path: "Library/Caches/com.other.app/blob", Size: 16 << 10, MTime: old},
			{Path: "Library/Logs/app/log.txt", Size: 4 << 10, MTime: old},
			{Path: "go/pkg/mod/github.com/x/y.go", Size: 8 << 10, MTime: old},
			{Path: "Downloads/Old.dmg", Size: 4 << 10, MTime: old},
			{Path: ".ollama/models/blobs/sha", Size: 4 << 10, MTime: old},
		},
		Dirs: []testutil.Dir{
			{Path: "Library/Caches/com.example.app", MTime: old},
			{Path: "Library/Caches/com.other.app", MTime: old},
		},
	}
}

type fx struct {
	t     *testing.T
	home  string
	store receipt.Store
	cfg   Config
}

// newFx builds a fixture home (with a Trash) and the model's config for
// it, as cmd/internal/guilaunch would for a KIPPLE_HOME.
func newFx(t *testing.T, spec testutil.Spec) *fx {
	t.Helper()
	spec.Dirs = append(spec.Dirs, testutil.TrashDirs()...)
	home := testutil.NewHome(t, spec)
	fi, err := os.Lstat(home)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, _ := policy.Identity(fi)
	env := policy.Env{Home: home, HomeIdentity: &policy.FileIdentity{Dev: dev, Ino: ino}}
	store, err := receipt.Open(testutil.StateDir(home))
	if err != nil {
		t.Fatal(err)
	}
	rs := rules.Catalogue()
	cfg := Config{
		Version: "test", Home: home, HomeLabel: "KIPPLE_HOME …/x/home", TestHome: true, GitAvailable: true,
		Scan:  scan.Options{Env: env, Rules: rs, KippleVersion: "test"},
		Apply: apply.Options{Env: env, Rules: rs, Receipts: store, KippleVersion: "test", Procs: idle{}},
		Store: store,
	}
	return &fx{t: t, home: home, store: store, cfg: cfg}
}

func (f *fx) model() *Model { return New(f.cfg) }

// scanned returns a model that has scanned the fixture.
func (f *fx) scanned() *Model {
	f.t.Helper()
	m := f.model()
	m.ScanNow()
	if m.Screen() != ScreenReview {
		f.t.Fatalf("screen %d after scan: %+v", m.Screen(), m.Problem())
	}
	return m
}

// rowOf returns the row index of the item whose path ends with rel.
func rowOf(t *testing.T, m *Model, rel string) int {
	t.Helper()
	for i, r := range m.Rows() {
		if r.Kind == RowItem && strings.HasSuffix(m.Plan().Items[r.Item].Path, "/"+rel) {
			return i
		}
	}
	t.Fatalf("no row for %s", rel)
	return -1
}

func itemAt(m *Model, row int) plan.Item { return m.Plan().Items[m.Rows()[row].Item] }

// fakePlan scans nothing: the model is given p as the scan's result.
func fakePlan(p plan.Plan) func(context.Context, scan.Options) (plan.Plan, error) {
	return func(context.Context, scan.Options) (plan.Plan, error) { return p, nil }
}

func pItem(id, rule, tier, path string, bytes int64, selected bool, reasons ...string) plan.Item {
	if reasons == nil {
		reasons = []string{}
	}
	return plan.Item{ID: id, Rule: rule, Tier: tier, Path: path, AllocatedBytes: bytes, Selected: selected, Reasons: reasons}
}

// countingGit is a git inspector that never runs git.
type countingGit struct{ n *int }

func (g countingGit) Inspect(context.Context, string) gitstate.Result {
	*g.n++
	return gitstate.Result{State: gitstate.StateUnavailable, Detail: "no Command Line Tools"}
}

// unavailableNote is what the confirmation says when the Trash cannot be
// opened for lack of permission: the Full Disk Access story on macOS, the
// error itself elsewhere.
func unavailableNote(note string) bool {
	if runtime.GOOS == "darwin" {
		return note == UnavailableNote
	}
	return strings.HasPrefix(note, "kipple can't use the Trash folder here, so it can't move anything: ") && !strings.Contains(note, "Full Disk Access")
}

// breakTrash makes the fixture's Trash unusable for a reason other than
// permission: ~/.Trash removed on macOS (kipple never creates it), a file
// where the freedesktop Trash folder should be on Linux.
func breakTrash(t *testing.T, home string) {
	t.Helper()
	if err := os.RemoveAll(testutil.Abs(home, testutil.TrashRoot)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "darwin" {
		if err := os.WriteFile(testutil.Abs(home, testutil.TrashRoot), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// homeMover is the receipt mover of a run into this platform's Trash.
var homeMover = map[bool]string{true: plan.MoverFreedesktop, false: plan.MoverRename}[runtime.GOOS == "linux"]
