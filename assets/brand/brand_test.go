package brand

import (
	"bytes"
	"os"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func TestAssetsResolveCurrentColor(t *testing.T) {
	for _, c := range []struct {
		name string
		svg  []byte
		ink  string
	}{
		{"mark light", Mark(false), "#10221E"}, {"mark dark", Mark(true), "#E3EBE4"},
		{"wordmark light", Wordmark(false), "#10221E"}, {"wordmark dark", Wordmark(true), "#E3EBE4"},
	} {
		if bytes.Contains(c.svg, []byte("currentColor")) || !bytes.Contains(c.svg, []byte(`fill="`+c.ink+`"`)) {
			t.Errorf("%s: currentColor not replaced with %s", c.name, c.ink)
		}
	}
	if !bytes.HasPrefix(Icon(), []byte("\x89PNG")) {
		t.Error("the icon is not a PNG")
	}
}
