//go:build darwin

// APFS stores names as given but looks them up normalisation-insensitively,
// so an NFD spelling reaches an NFC-stored folder. Linux file systems
// compare bytes: there the NFD spelling names no folder at all, and the
// respelling this test checks cannot arise.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
)

// "café" with a precomposed é (NFC) and with e plus a combining acute
// accent (NFD).
const (
	nfcName = "caf\u00e9"
	nfdName = "cafe\u0301"
)

// Addendum 3: a KIPPLE_HOME spelled in NFD for a folder stored in NFC is
// respelled to the stored bytes, so plan.home matches what the policy sees.
func TestNFDHomeIsRespelledToStoredNFC(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nfc := filepath.Join(base, nfcName)
	if err := os.MkdirAll(filepath.Join(nfc, "Library/Logs/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	names, _ := os.ReadDir(base)
	if len(names) != 1 || names[0].Name() != nfcName {
		t.Skip("this volume does not store the name as given (not APFS?)")
	}
	nfd := filepath.Join(base, nfdName)
	if nfd == nfc {
		t.Fatal("test bug: NFC and NFD spellings are equal")
	}
	t.Setenv("KIPPLE_HOME", nfd)
	t.Setenv("KIPPLE_STATE_DIR", filepath.Join(base, "state"))
	testutil.GuardRealHome(t)
	code, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	if code != exitOK || p.Home != nfc {
		t.Fatalf("exit %d, plan home %q (% x), want the stored NFC %q", code, p.Home, p.Home, nfc)
	}
}
