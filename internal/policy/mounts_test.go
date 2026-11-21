//go:build unix

package policy

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func identityAt(t *testing.T, p string) FileIdentity {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := identityOf(fi)
	if !ok {
		t.Fatal("no identity")
	}
	return FileIdentity{Dev: id.dev, Ino: id.ino}
}

// specs/safety-policy "Mount points are boundaries", with an injected
// mount oracle (a same-device bind mount cannot be made unprivileged; the
// privileged check in make test-linux-mounts uses real ones).
func TestMountPointsAreBoundaries(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{
		{Path: ".cache/foo/docs/letter.txt", Content: "x"},
		{Path: ".cache/foo/blob", Content: "x"},
		{Path: ".cache/bar/blob", Content: "x"},
		{Path: ".cache/plain/blob", Content: "x"},
	}})
	cache := StaticRule{ID: "xdg.cache", Roots: []string{".cache"}, Kind: MatchChildren}
	docs := StaticRule{ID: "x", Roots: []string{".cache/foo"}, Kind: MatchChildren}
	mounts := StubMounts{Roots: map[FileIdentity]bool{
		identityAt(t, home+"/.cache/foo/docs"): true,
		identityAt(t, home+"/.cache/bar"):      true,
	}}
	env := Env{Home: home, Mounts: mounts}
	for _, tc := range []struct {
		name string
		rule Rule
		path string
		want string
	}{
		{"Candidate is a bind mount point", cache, home + "/.cache/bar", ReasonDifferentDevice},
		{"Mount point between home and the candidate", docs, home + "/.cache/foo/docs", ReasonDifferentDevice},
		{"Below a mount point", StaticRule{ID: "y", Roots: []string{".cache/foo/docs"}, Kind: MatchChildren}, home + "/.cache/foo/docs/letter.txt", ReasonDifferentDevice},
		{"Ordinary folder", cache, home + "/.cache/plain", ""},
		{"Folder holding a mount point is itself allowed (the size walk marks it)", cache, home + "/.cache/foo", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := authorizeBoth(t, env, tc.rule, tc.path)
			if got.Reason != tc.want || got.Allowed != (tc.want == "") {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
		})
	}
	t.Run("Session.Open refuses a path through a mount point", func(t *testing.T) {
		s, err := NewSession(env)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close() }()
		var re *RefusedError
		if _, err := s.Open(home + "/.cache/bar/blob"); !errors.As(err, &re) || re.Reason != ReasonDifferentDevice {
			t.Fatalf("err = %v", err)
		}
	})
}

// specs/safety-policy "btrfs subvolume under home": a subvolume has its own
// device number, so everything under it is refused as on another device.
func TestBtrfsSubvolumeUnderHome(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".cache/pip/http/blob", Content: "x"}}})
	sub := home + "/.cache"
	orig := deviceOf
	t.Cleanup(func() { deviceOf = orig })
	deviceOf = func(path string, fi fs.FileInfo) (uint64, bool) {
		dev, ok := orig(path, fi)
		if path == sub || strings.HasPrefix(path, sub+"/") {
			return dev + 1, ok
		}
		return dev, ok
	}
	pip := StaticRule{ID: "pip.cache", Roots: []string{".cache/pip"}, Kind: MatchChildren}
	if got := authorizeBoth(t, Env{Home: home}, pip, home+"/.cache/pip/http"); got.Reason != ReasonDifferentDevice {
		t.Fatalf("want %q, got %+v", ReasonDifferentDevice, got)
	}
}

// When mount points cannot be determined, the session still walks (scan
// lists and marks items) but reports it; apply and restore refuse.
func TestMountsUnavailable(t *testing.T) {
	home := testutil.NewHome(t, testutil.Spec{Files: []testutil.File{{Path: ".cache/a/blob", Content: "x"}}})
	s, err := NewSession(Env{Home: home, Mounts: StubMounts{Err: ErrMountsUnavailable}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if !errors.Is(s.MountsErr(), ErrMountsUnavailable) {
		t.Fatalf("MountsErr = %v", s.MountsErr())
	}
	if _, err := s.MountRoot(-1, "a", 1, 2); !errors.Is(err, ErrMountsUnavailable) {
		t.Fatalf("MountRoot err = %v", err)
	}
}
