package procs

import (
	"os"
	"slices"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func TestParsers(t *testing.T) {
	ids, err := parseLsappinfo([]byte(`1) "Finder" ASN:0x0-0x1001:
    bundleID="com.apple.finder"
2) "Xcode" ASN:0x0-0x2002:
    bundleID="com.apple.dt.Xcode"
`))
	if err != nil || !ids["com.apple.finder"] || !ids["com.apple.dt.Xcode"] || len(ids) != 2 {
		t.Fatalf("lsappinfo: %v %v", ids, err)
	}
	if _, err := parseLsappinfo([]byte("nothing here")); err == nil {
		t.Fatal("empty lsappinfo output must be unknown")
	}
	pids, err := parsePIDs([]byte("12\n345\n"))
	if err != nil || !slices.Equal(pids, []int{12, 345}) {
		t.Fatalf("pgrep: %v %v", pids, err)
	}
	if _, err := parsePIDs([]byte("12\nbogus\n")); err == nil {
		t.Fatal("garbage pgrep output accepted")
	}
	cwds := parseLsofCwds([]byte("p12\nfcwd\nn/Users/me/dev/app\np345\nfcwd\nn/tmp\n"))
	if !slices.Equal(cwds, []string{"/Users/me/dev/app", "/tmp"}) {
		t.Fatalf("lsof: %v", cwds)
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		dir, project string
		want         bool
	}{
		{"/u/dev/app", "/u/dev/app", true},
		{"/u/dev/app/src", "/u/dev/app", true},
		{"/U/Dev/App/src", "/u/dev/app", true},
		{"/u/dev/apple", "/u/dev/app", false},
		{"/u/dev", "/u/dev/app", false},
		{"/u/dev/app", "", false},
	}
	for _, tc := range cases {
		if got := Within(tc.dir, tc.project); got != tc.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tc.dir, tc.project, got, tc.want)
		}
	}
}
