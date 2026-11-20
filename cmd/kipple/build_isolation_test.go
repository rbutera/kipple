package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// goList runs go list -deps from the repository root.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	cmd := exec.Command(gobin, append([]string{"list", "-deps"}, args...)...)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, out)
	}
	return strings.Fields(string(out))
}

// Spec scenario "Default build excludes GUI" (gui): go build ./... without
// the gui tag compiles no Fyne package, and the tagged GUI does use Fyne,
// so the check cannot pass by accident.
func TestDefaultBuildHasNoFyne(t *testing.T) {
	for _, p := range goList(t, "./...") {
		if strings.HasPrefix(p, "fyne.io/") || strings.Contains(p, "go-gl/") {
			t.Errorf("the default build compiles %s", p)
		}
	}
	found := false
	for _, p := range goList(t, "-tags", "gui", "./cmd/kipple-gui") {
		found = found || p == "fyne.io/fyne/v2"
	}
	if !found {
		t.Fatal("go list -tags gui ./cmd/kipple-gui does not list fyne.io/fyne/v2")
	}
}
