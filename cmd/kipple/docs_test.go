package main

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/policy"
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

var updateDocs = flag.Bool("update-docs", false, "rewrite generated sections of docs/")

// denylistSection renders policy.Denylist() as the docs/safety.md section.
func denylistSection() string {
	var b strings.Builder
	b.WriteString("<!-- denylist:start -->\n")
	for _, e := range policy.Denylist() {
		b.WriteString("- `" + e + "`\n")
	}
	b.WriteString("<!-- denylist:end -->")
	return b.String()
}

// Task 3.8: the denylist in docs/safety.md is generated from
// policy.Denylist() and must match it exactly; every apply and restore
// skip reason is documented there.
func TestSafetyDoc(t *testing.T) {
	const path = "../../docs/safety.md"
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := regexp.MustCompile(`(?s)<!-- denylist:start -->.*?<!-- denylist:end -->`)
	if !section.Match(doc) {
		t.Fatal("docs/safety.md has no denylist section markers")
	}
	want := denylistSection()
	if *updateDocs {
		doc = section.ReplaceAll(doc, []byte(want))
		if err := os.WriteFile(path, doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(section.Find(doc)); got != want {
		t.Fatalf("docs/safety.md denylist differs from policy.Denylist() (go test ./cmd/kipple -run TestSafetyDoc -update-docs):\n%s", want)
	}
	item := map[string]bool{}
	for _, r := range plan.Reasons() {
		item[r] = true
	}
	for _, r := range plan.SkipReasons() {
		if !item[r] && !strings.Contains(string(doc), "| `"+r+"` |") {
			t.Errorf("skip reason %q is not documented in docs/safety.md", r)
		}
	}
}
