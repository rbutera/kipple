package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
)

// Task 2.6: the example in docs/plan-schema.md validates against the schema
// that kipple schema plan prints, and the doc documents every item reason.
func TestPlanSchemaDoc(t *testing.T) {
	doc, err := os.ReadFile("../../docs/plan-schema.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(blocks) != 1 {
		t.Fatalf("want exactly one json example, found %d", len(blocks))
	}
	example := blocks[0][1]
	code, schema, _ := cli(t, "schema", "plan")
	if code != exitOK {
		t.Fatalf("schema exit %d", code)
	}
	if err := plantest.ValidateWith([]byte(schema), "plan", example); err != nil {
		t.Fatalf("docs example does not validate against kipple schema plan: %v", err)
	}
	p, err := plan.Decode(strings.NewReader(string(example)))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range p.Items {
		if want := plan.ID(it.Rule, strings.TrimPrefix(it.Path, p.Home+"/")); it.ID != want {
			t.Errorf("example item %s has ID %s, want %s", it.Path, it.ID, want)
		}
	}
	text := string(doc)
	for _, r := range plan.Reasons() {
		if strings.HasPrefix(r, plan.RefusedPrefix) {
			continue
		}
		if !strings.Contains(text, "| `"+r+"` |") {
			t.Errorf("reason %q is not documented in the reasons table", r)
		}
	}
	if !strings.Contains(text, "`refused:<reason>`") {
		t.Error("refused:<reason> is not documented")
	}
}
