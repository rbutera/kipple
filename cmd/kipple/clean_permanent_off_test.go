//go:build !kipple_permanent

package main

import (
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

// specs/clean-apply "Permanent refused in v0.1": --permanent is parsed but
// refused with a usage error, with or without --apply, and nothing moves.
func TestPermanentRefusedInThisRelease(t *testing.T) {
	home := cleanFixture(t)
	before := testutil.Checksum(t, home)
	for _, args := range [][]string{
		{"clean", "--apply", "--permanent"},
		{"clean", "--permanent"},
		{"clean", "--apply", "--permanent", "--level", "review", "--json"},
	} {
		code, out, errOut := cli(t, args...)
		if code != exitUsage || !strings.Contains(errOut, "permanent deletion is not available in this release") || !strings.Contains(errOut, "docs/safety.md#permanent") {
			t.Fatalf("%v: exit %d stderr %q", args, code, errOut)
		}
		if args[len(args)-1] == "--json" {
			var env errorEnvelope
			exactlyOneJSON(t, out, &env)
			if env.Error.Code != codeUsage {
				t.Fatalf("envelope %+v", env)
			}
		}
	}
	if testutil.Checksum(t, home) != before {
		t.Fatal("something moved")
	}
}
