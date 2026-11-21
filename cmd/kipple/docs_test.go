package main

import (
	"encoding/json"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rbutera/kipple/cmd/internal/launch"
	"github.com/rbutera/kipple/internal/output"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/plan/plantest"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/rules"
)

// Spec scenario "Documented compatibility rule" (cli-agent-interface):
// docs/plan-schema.md states that new fields, enum values and error codes
// are compatible and that renames, removals and changes of meaning bump
// schema_version; its Linux receipt example validates against kipple
// schema receipt, with mover freedesktop.
func TestDocumentedCompatibilityRule(t *testing.T) {
	doc, err := os.ReadFile("../../docs/plan-schema.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(doc)), " ")
	for _, want := range []string{"Adding a field, a new enum value", "a new error code", "is compatible and keeps the version", "renaming or removing a field, or changing what a field, value or code means, bumps it", "`freedesktop`", "`unsupported_platform`", "`mounts_unavailable`"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/plan-schema.md lacks %q", want)
		}
	}
	blocks := regexp.MustCompile("(?s)```json receipt\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(blocks) != 1 {
		t.Fatalf("want one Linux receipt example, found %d", len(blocks))
	}
	_, schema, _ := cli(t, "schema", "receipt")
	if err := plantest.ValidateWith([]byte(schema), "receipt", blocks[0][1]); err != nil {
		t.Fatalf("the Linux receipt example does not validate against kipple schema receipt: %v", err)
	}
	if !strings.Contains(string(blocks[0][1]), `"mover": "freedesktop"`) {
		t.Fatal("the example's mover is not freedesktop")
	}
	var rc plan.Receipt
	if err := json.Unmarshal(blocks[0][1], &rc); err != nil {
		t.Fatal(err)
	}
	for _, m := range rc.Moved {
		if want := plan.ID(m.Rule, strings.TrimPrefix(m.Path, rc.Home+"/")); m.ID != want {
			t.Errorf("example item %s has ID %s, want %s", m.Path, m.ID, want)
		}
	}
}

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

// xdgCacheExcludesSection renders rules.XDGCacheExcludes() as the
// docs/safety.md table.
func xdgCacheExcludesSection() string {
	var b strings.Builder
	b.WriteString("<!-- xdg-cache-excludes:start -->\n")
	b.WriteString("| Child of `~/.cache` | Why it is never offered |\n|---|---|\n")
	for _, e := range rules.XDGCacheExcludes() {
		b.WriteString("| `" + e.Pattern + "` | " + e.Why + " |\n")
	}
	b.WriteString("<!-- xdg-cache-excludes:end -->")
	return b.String()
}

// Spec scenario "Excludes documented": every xdg.cache exclude is in
// docs/safety.md with its reason, generated from the rule's table.
func TestXDGCacheExcludesDocumented(t *testing.T) {
	const path = "../../docs/safety.md"
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := regexp.MustCompile(`(?s)<!-- xdg-cache-excludes:start -->.*?<!-- xdg-cache-excludes:end -->`)
	if !section.Match(doc) {
		t.Fatal("docs/safety.md has no xdg.cache exclude section markers")
	}
	want := xdgCacheExcludesSection()
	if *updateDocs {
		doc = section.ReplaceAll(doc, []byte(want))
		if err := os.WriteFile(path, doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(section.Find(doc)); got != want {
		t.Fatalf("docs/safety.md xdg.cache excludes differ from rules.XDGCacheExcludes() (go test ./cmd/kipple -run TestXDGCacheExcludesDocumented -update-docs):\n%s", want)
	}
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

// Task 3.5 (wave 3 brief, "docs/safety.md consistency check by test"):
// the Linux facts the README, docs/safety.md, llms.txt and SECURITY.md
// state come from the code: the Trash folder, the default state folder,
// the receipt mover, the new error codes, and the post-move advice.
func TestLinuxDocsMatchBehaviour(t *testing.T) {
	trashDir := output.PhrasesFor("linux").TrashFolder
	state := launch.DefaultStateDir("linux", "~") + "/receipts"
	docs := map[string][]string{
		"README.md": {trashDir, state, `"mover":` + "\n" + `"freedesktop"`, codeMountsUnavailable, codeUnsupportedPlatform,
			"findmnt -R ~/.local/share/Trash", "user.com.rbutera.kipple", "already_restored", "XDG_CACHE_HOME", "`kipple tui`",
			`grep " kipple_${VERSION}_linux_${ARCH}.tar.gz\$" checksums.txt | sha256sum -c -`, `kipple_${VERSION}_linux_${ARCH}.tar.gz`, "Platform: linux/amd64"},
		"docs/safety.md": {trashDir, "~/.local/state/kipple/receipts", "`" + codeMountsUnavailable + "`", "findmnt -R ~/.local/share/Trash",
			"user.com.rbutera.kipple", "RENAME_NOREPLACE", "246 to 255 bytes", "already_restored"},
		"llms.txt":    {"`" + plan.MoverFreedesktop + "`", "`" + codeMountsUnavailable + "`", "`" + codeUnsupportedPlatform + "`", "`info_ino`", "~/.local/state/kipple", "`os` and `arch`"},
		"SECURITY.md": {"`" + plan.MoverFreedesktop + "`", "~/.local/state/kipple/receipts", "findmnt -R"},
	}
	for file, wants := range docs {
		text := readRepoFile(t, file)
		for _, w := range wants {
			if !strings.Contains(text, w) {
				t.Errorf("%s lacks %q", file, w)
			}
		}
	}
	// The Linux state folder in the docs is the one the code uses.
	if state != "~/.local/state/kipple/receipts" {
		t.Fatalf("DefaultStateDir on Linux is %s; update the docs", state)
	}
}

// Spec scenario "Linux install documented" (distribution), README side:
// the Linux steps verify the signature and the checksum before installing
// and end with kipple version; make test-linux-tarball runs them.
func TestLinuxInstallDocumented(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	i := strings.Index(readme, "### Release binary on Linux")
	if i < 0 {
		t.Fatal("README has no Linux install section")
	}
	block := regexp.MustCompile("(?s)```sh\n(.*?)```").FindStringSubmatch(readme[i:])
	if block == nil {
		t.Fatal("the Linux install section has no sh block")
	}
	steps := block[1]
	order := []string{"cosign verify-blob", "| sha256sum -c -", `tar -xzf "kipple_${VERSION}_linux_${ARCH}.tar.gz" kipple`, "kipple version"}
	last := -1
	for _, s := range order {
		k := strings.Index(steps, s)
		if k <= last {
			t.Fatalf("the Linux steps lack %q or have it out of order", s)
		}
		last = k
	}
	if !strings.HasSuffix(strings.TrimSpace(steps), "kipple version") {
		t.Fatal("the Linux steps do not end with kipple version")
	}
	mk := readRepoFile(t, "Makefile")
	mustContain(t, "Makefile", mk, "test-linux-tarball:", "scripts/linux-tarball-check.sh dist")
	sc := readRepoFile(t, "scripts/linux-tarball-check.sh")
	mustContain(t, "scripts/linux-tarball-check.sh", sc, "linux-tarball-steps.sh", "scan --json")
	stepsScript := readRepoFile(t, "scripts/linux-tarball-steps.sh")
	mustContain(t, "scripts/linux-tarball-steps.sh", stepsScript, "#!/bin/sh", "### Release binary on Linux", "Platform: linux/$ARCH")
	mustContain(t, "Makefile", mk, "ALPINE_IMAGE ?= alpine:3.20@sha256:", "sh scripts/linux-tarball-steps.sh dist")
}
