//go:build darwin

// APFS folds case and Unicode when it looks a name up: a long s or an NFD
// spelling reaches the real home under another name. Linux file systems
// compare bytes, so these spellings name nothing there and the identity
// check has nothing to catch; the portable cases are in
// TestGuardRealHomeByIdentity.

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuardRealHomeByAPFSAlias(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "Users", "mé") // stored precomposed (NFC)
	if err := os.MkdirAll(filepath.Join(real, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
	}{
		{"Long s spelling inside the real home", filepath.Join(base, "Uſers", "mé", "Documents")},
		{"NFD spelling inside the real home", filepath.Join(base, "Users", "mé", "Documents")},
		{"Long s spelling of an ancestor", filepath.Join(base, "Uſers")},
		{"Missing folder inside a long s spelling of the real home", filepath.Join(base, "Uſers", "mé", "state")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkIdentity("KIPPLE_HOME", tc.path, real); err == nil {
				t.Fatalf("checkIdentity(%q) passed, want a refusal", tc.path)
			}
		})
	}
}
