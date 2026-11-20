//go:build kipple_permanent

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
)

// Spec scenario "Permanent on review item", through the CLI.
func TestCleanPermanentOnReviewItem(t *testing.T) {
	home := cleanFixture(t)
	code, out, _ := cli(t, "clean", "--apply", "--permanent", "--level", "review", "--json")
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	if code != exitPartial || len(res.Moved) != 2 || len(res.Skipped) != 1 || res.Skipped[0].Reason != plan.SkipPermanentNotAllowed {
		t.Fatalf("exit %d result %+v", code, res)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".Trash")); len(entries) != 0 {
		t.Fatal("--permanent used the Trash")
	}
}
