package homeguard_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

// tree makes base/Users/<name>/Documents/OldMac plus a separate sibling and
// returns base and the stand-in real home. Nothing outside t.TempDir.
func tree(t *testing.T, name string) (base, real string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real = filepath.Join(base, "Users", name)
	for _, d := range []string{filepath.Join(real, "Documents", "OldMac"), filepath.Join(base, "fixture")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return base, real
}

// exists skips the case when a spelling does not reach a directory on this
// volume (a case- or normalisation-sensitive file system).
func exists(t *testing.T, p string) {
	t.Helper()
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Skipf("spelling %q does not resolve on this volume: %v", p, err)
	}
}

func TestRelateOnDisk(t *testing.T) {
	base, real := tree(t, "me")
	nfdBase, nfcReal := tree(t, "mé") // precomposed é
	cases := []struct {
		name      string
		candidate string
		real      string
		want      homeguard.Relation
	}{
		{"Separate fixture", filepath.Join(base, "fixture"), real, homeguard.Separate},
		{"Real home itself", real, real, homeguard.Same},
		{"Inside the real home", filepath.Join(real, "Documents", "OldMac"), real, homeguard.Inside},
		{"Ancestor of the real home", filepath.Join(base, "Users"), real, homeguard.Contains},
		{"Filesystem root", "/", real, homeguard.Contains},
		{"Long s spelling of the real home", filepath.Join(base, "Uſers", "me"), real, homeguard.Same},
		{"Long s spelling inside the real home", filepath.Join(base, "Uſers", "me", "Documents", "OldMac"), real, homeguard.Inside},
		{"NFD spelling inside the real home", filepath.Join(nfdBase, "Users", "mé", "Documents"), nfcReal, homeguard.Inside},
		{"Upper-case spelling of an ancestor", filepath.Join(base, "USERS"), real, homeguard.Contains},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exists(t, tc.candidate)
			got, err := homeguard.Relate(homeguard.System(), tc.candidate, tc.real)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Relate(%q, %q) = %v, want %v", tc.candidate, tc.real, got, tc.want)
			}
		})
	}
}

func TestRelateFailsClosed(t *testing.T) {
	base, real := tree(t, "me")
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "fixture"), link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, candidate, real string }{
		{"Candidate missing", filepath.Join(base, "missing"), real},
		{"Real home missing", filepath.Join(base, "fixture"), filepath.Join(base, "Users", "nobody")},
		{"Candidate is a symlink", link, real},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := homeguard.Relate(homeguard.System(), tc.candidate, tc.real); err == nil {
				t.Fatal("no error")
			}
		})
	}
}

// stub models a volume with firmlinks: paths map to identity chains, and
// stats map to identities. Unknown paths do not exist.
type stub struct {
	chains map[string][]homeguard.ID
	stats  map[string]homeguard.ID
	mounts map[string]string
	err    error
}

func (s stub) fs() homeguard.FS {
	return homeguard.FS{
		Chain: func(dir string, _ bool) ([]homeguard.ID, error) {
			if s.err != nil {
				return nil, s.err
			}
			c, ok := s.chains[dir]
			if !ok {
				return nil, os.ErrNotExist
			}
			return c, nil
		},
		Stat: func(p string) (homeguard.ID, bool, error) {
			id, ok := s.stats[p]
			return id, ok, nil
		},
		MountPoint: func(p string) (string, error) { return s.mounts[p], nil },
	}
}

func TestRelateFirmlinks(t *testing.T) {
	var (
		root   = homeguard.ID{Dev: 1, Ino: 2}
		users  = homeguard.ID{Dev: 1, Ino: 10}
		me     = homeguard.ID{Dev: 1, Ino: 11}
		docs   = homeguard.ID{Dev: 1, Ino: 12}
		data   = homeguard.ID{Dev: 1, Ino: 90}
		sysVol = homeguard.ID{Dev: 1, Ino: 91}
		sys    = homeguard.ID{Dev: 1, Ino: 92}
	)
	s := stub{
		chains: map[string][]homeguard.ID{
			"/Users/me": {me, users, root},
			// A firmlinked spelling has the same identities as /Users/me.
			"/System/Volumes/Data/Users/me":           {me, users, root},
			"/System/Volumes/Data/Users/me/Documents": {docs, me, users, root},
			"/System/Volumes/Data/Users":              {users, root},
			// The Data volume root is not on /Users/me's ".." chain, and
			// neither are its ancestors.
			"/System/Volumes/Data": {data, sysVol, sys, root},
			"/System/Volumes":      {sysVol, sys, root},
			"/System":              {sys, root},
			"/Library":             {{Dev: 1, Ino: 93}, root},
		},
		mounts: map[string]string{"/Users/me": "/System/Volumes/Data"},
		stats: map[string]homeguard.ID{
			"/System/Volumes/Data/Users/me": me,
		},
	}
	for _, tc := range []struct {
		name, candidate string
		want            homeguard.Relation
	}{
		{"Firmlink spelling of the real home", "/System/Volumes/Data/Users/me", homeguard.Same},
		{"Firmlink spelling inside the real home", "/System/Volumes/Data/Users/me/Documents", homeguard.Inside},
		{"Firmlink spelling of an ancestor", "/System/Volumes/Data/Users", homeguard.Contains},
		{"Data volume root reaches the real home by a firmlink", "/System/Volumes/Data", homeguard.Contains},
		{"Parent of the home's mount point", "/System/Volumes", homeguard.Contains},
		{"Grandparent of the home's mount point", "/System", homeguard.Contains},
		{"Unrelated system folder", "/Library", homeguard.Separate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := homeguard.Relate(s.fs(), tc.candidate, "/Users/me")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("Lookup error fails closed", func(t *testing.T) {
		bad := s
		bad.err = errors.New("boom")
		if _, err := homeguard.Relate(bad.fs(), "/System/Volumes/Data", "/Users/me"); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("Mount point lookup error fails closed", func(t *testing.T) {
		bad := s.fs()
		bad.MountPoint = func(string) (string, error) { return "", errors.New("statfs") }
		if _, err := homeguard.Relate(bad, "/Library", "/Users/me"); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("Incomplete FS fails closed", func(t *testing.T) {
		if _, err := homeguard.Relate(homeguard.FS{}, "/a", "/b"); err == nil {
			t.Fatal("no error")
		}
	})
}
