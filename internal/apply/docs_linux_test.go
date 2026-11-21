//go:build linux

package apply

import (
	"os"
	"strings"
	"testing"
)

// The extended attribute the docs tell Linux users about is the one apply
// writes and restore requires.
func TestTagNameDocumented(t *testing.T) {
	for _, f := range []string{"../../docs/safety.md", "../../README.md"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), tagName) {
			t.Errorf("%s does not name the tag %s", f, tagName)
		}
	}
}
