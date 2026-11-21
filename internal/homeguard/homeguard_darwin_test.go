//go:build darwin

// APFS folds case and Unicode when it looks a name up, so a long s, an NFD
// or an upper-case spelling reaches the real home under another name, and
// Relate must see through it by identity. Linux file systems compare
// bytes: there these spellings name nothing at all.

package homeguard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/homeguard"
)

// exists skips the case when a spelling does not reach a directory on this
// volume (a case- or normalisation-sensitive file system).
func exists(t *testing.T, p string) {
	t.Helper()
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Skipf("spelling %q does not resolve on this volume: %v", p, err)
	}
}

func TestRelateOnDiskAPFSAliases(t *testing.T) {
	base, real := tree(t, "me")
	nfdBase, nfcReal := tree(t, "mé") // precomposed é
	cases := []struct {
		name      string
		candidate string
		real      string
		want      homeguard.Relation
	}{
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
