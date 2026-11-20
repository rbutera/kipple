package receipt

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)

func sample(home string, created time.Time) plan.Receipt {
	return plan.Receipt{
		KippleVersion: "test",
		CreatedAt:     created,
		Complete:      true,
		Home:          home,
		Level:         "safe",
		TrashDir:      home + "/.Trash",
		PlanGenerated: created.Add(-time.Minute),
		Moved: []plan.MovedItem{
			{ID: "aaaaaaaaaaaa", Rule: "caches.user", Path: home + "/Library/Caches/a", TrashPath: home + "/.Trash/a", Bytes: 4096, Dev: 1, Ino: 2, MovedAt: created},
			{ID: "bbbbbbbbbbbb", Rule: "logs.user", Path: home + "/Library/Logs/b", TrashPath: home + "/.Trash/b", Bytes: 8192, Dev: 1, Ino: 3, MovedAt: created},
		},
		Skipped: []plan.SkippedItem{
			{ID: "cccccccccccc", Rule: "caches.user", Path: home + "/Library/Caches/c", Reason: plan.ReasonOwnerRunning},
		},
	}
}

func store(t *testing.T) Store {
	t.Helper()
	s, err := Open(testutil.StateDir(testutil.NewHome(t, testutil.Spec{})))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Spec scenario "Receipt written" (clean-apply), at the store level: two
// moved entries and one skipped entry are written to the state directory.
func TestReceiptWritten(t *testing.T) {
	s := store(t)
	r := sample("/fx/home", t0)
	path, err := s.Create(&r)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(r.ID) || path != filepath.Join(s.Dir(), r.ID+".json") {
		t.Fatalf("id %q path %q", r.ID, path)
	}
	if !strings.HasPrefix(r.ID, "20261120T093000Z-") {
		t.Fatalf("id %q does not start with the creation time", r.ID)
	}
	e, err := s.Load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Receipt.Moved) != 2 || len(e.Receipt.Skipped) != 1 || e.Receipt.SchemaVersion != plan.SchemaVersion {
		t.Fatalf("round trip: %+v", e.Receipt)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode: %v %v", fi.Mode(), err)
	}
	entries, _ := os.ReadDir(s.Dir())
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestListNewestFirst(t *testing.T) {
	s := store(t)
	var ids []string
	for i, at := range []time.Time{t0, t0.Add(2 * time.Hour), t0.Add(time.Hour)} {
		r := sample("/fx/home", at)
		r.Moved = r.Moved[:i%2+1]
		if _, err := s.Create(&r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	// Noise: not receipts, or not valid ones.
	_ = os.WriteFile(filepath.Join(s.Dir(), "notes.txt"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(s.Dir(), "20260101T000000Z-zzzzzz.json"), []byte("{"), 0o600)
	list, errs := s.List()
	if len(errs) != 1 {
		t.Fatalf("want one error for the corrupt receipt, got %v", errs)
	}
	got := []string{}
	for _, e := range list {
		got = append(got, e.Receipt.ID)
	}
	want := []string{ids[1], ids[2], ids[0]}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestEmptyStore(t *testing.T) {
	s := store(t)
	list, errs := s.List()
	if len(list) != 0 || len(errs) != 0 {
		t.Fatalf("list %v errs %v", list, errs)
	}
	if _, err := os.Stat(s.Dir()); !os.IsNotExist(err) {
		t.Fatal("List created the receipts folder")
	}
}

func TestSaveAnnotatesRestored(t *testing.T) {
	s := store(t)
	r := sample("/fx/home", t0)
	if _, err := s.Create(&r); err != nil {
		t.Fatal(err)
	}
	if !MarkRestored(&r, "bbbbbbbbbbbb", t0.Add(time.Hour)) {
		t.Fatal("item not found")
	}
	if MarkRestored(&r, "zzzzzzzzzzzz", t0) {
		t.Fatal("unknown item marked")
	}
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
	e, err := s.Load(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Receipt.Moved[0].RestoredAt != nil || e.Receipt.Moved[1].RestoredAt == nil || !e.Receipt.Moved[1].RestoredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("restored annotation: %+v", e.Receipt.Moved)
	}
	if err := s.Save(plan.Receipt{ID: "../escape"}); err == nil {
		t.Fatal("Save accepted an invalid ID")
	}
}

func TestUntrustedReceipts(t *testing.T) {
	s := store(t)
	r := sample("/fx/home", t0)
	if _, err := s.Create(&r); err != nil {
		t.Fatal(err)
	}
	t.Run("symlinked receipts folder is refused", func(t *testing.T) {
		home := testutil.NewHome(t, testutil.Spec{Dirs: []testutil.Dir{{Path: "elsewhere"}}})
		state := testutil.StateDir(home)
		if err := os.Symlink(filepath.Join(home, "elsewhere"), filepath.Join(state, DirName)); err != nil {
			t.Fatal(err)
		}
		s2, _ := Open(state)
		r2 := sample(home, t0)
		if _, err := s2.Create(&r2); err == nil {
			t.Fatal("wrote through a symlinked receipts folder")
		}
		if entries, _ := os.ReadDir(filepath.Join(home, "elsewhere")); len(entries) != 0 {
			t.Fatalf("receipt written into the symlink target: %v", entries)
		}
	})
	t.Run("symlinked receipt file is refused", func(t *testing.T) {
		link := filepath.Join(filepath.Dir(s.Dir()), "link.json")
		if err := os.Symlink(s.PathOf(r.ID), link); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(link); err == nil {
			t.Fatal("LoadFile followed a symlink")
		}
		if _, err := LoadFile(s.PathOf(r.ID)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unknown fields and other versions are refused", func(t *testing.T) {
		for _, doc := range []string{
			`{"schema_version":1,"surprise":true}`,
			`{"schema_version":2}`,
			`{"schema_version":1} {}`,
		} {
			if _, err := plan.DecodeReceipt(strings.NewReader(doc)); err == nil {
				t.Errorf("accepted %s", doc)
			}
		}
	})
	t.Run("IDs are validated", func(t *testing.T) {
		for _, id := range []string{"", "../x", "20261120T093000Z-ABCDEF", "20261120T093000Z-abcde"} {
			if _, err := s.Load(id); err == nil {
				t.Errorf("Load(%q) accepted", id)
			}
		}
	})
}

// Golden receipt JSON that validates against kipple schema receipt.
func TestGoldenReceipt(t *testing.T) {
	r := sample("/fx/home", t0)
	r.ID = "20261120T093000Z-abc123"
	restored := t0.Add(time.Hour)
	r.Moved[1].RestoredAt = &restored
	data, err := encode(&r)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "receipt.golden.json")
	if *update {
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("receipt differs from %s (go test -update):\n%s", golden, data)
	}
	if err := plantest.Validate("receipt", want); err != nil {
		t.Fatalf("golden receipt does not validate: %v", err)
	}
	bad := bytes.Replace(want, []byte(`"owner_running"`), []byte(`"felt_like_it"`), 1)
	if err := plantest.Validate("receipt", bad); err == nil {
		t.Fatal("an unknown skip reason validated")
	}
}
