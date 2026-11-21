package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/testutil"
	"github.com/rbutera/kipple/internal/trash"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile("../../" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustContain(t *testing.T, name, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s lacks %q", name, w)
		}
	}
}

// Spec scenario "Red CI blocks release" (distribution): the release
// workflow runs on v* tags, its first job checks the CI workflow's
// conclusion for the tagged commit and fails otherwise, and the job that
// publishes needs it. (actionlint and a goreleaser snapshot check the files
// themselves; this pins the contract.)
func TestRedCIBlocksRelease(t *testing.T) {
	wf := readRepoFile(t, ".github/workflows/release.yml")
	mustContain(t, "release.yml", wf,
		`tags: ["v*"]`,
		"ci-green:",
		"actions/workflows/ci.yml/runs?head_sha=${SHA}",
		// Only runs of this exact commit from a push or a manual run count;
		// a pull_request run tested a merge commit.
		`select(.head_sha == \"${SHA}\" and (.event == \"push\" or .event == \"workflow_dispatch\"))`,
		`.conclusion == "success"`,
		// Queued or running: poll. None yet: a short grace, then fail.
		`any(.[]; .status != "completed")`,
		"sleep 30",
		"Push the commit to main first",
		"exit 1",
		"needs: ci-green",
		"id-token: write",
		"contents: write",
		"sigstore/cosign-installer",
		"cosign-release: v2.6.5",
		"goreleaser/goreleaser-action",
	)
	if strings.Contains(wf, `.event == \"pull_request\"`) {
		t.Error("the release gate accepts pull_request runs")
	}
	if strings.Index(wf, "ci-green:") > strings.Index(wf, "release:\n") {
		t.Error("the CI check is not the first job")
	}
}

// Must-fix 3 (wave 4 review): every action in every workflow is pinned to a
// full commit SHA with its version as a trailing comment, and goreleaser's
// version is exact.
func TestActionsPinned(t *testing.T) {
	pinned := regexp.MustCompile(`^\s*(- )?uses: [\w.-]+/[\w.-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+$`)
	n := 0
	for _, wf := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		for _, line := range strings.Split(readRepoFile(t, wf), "\n") {
			if !strings.Contains(line, "uses:") {
				continue
			}
			n++
			if !pinned.MatchString(line) {
				t.Errorf("%s: not pinned to a commit SHA with a version comment: %s", wf, strings.TrimSpace(line))
			}
		}
	}
	if n < 5 {
		t.Fatalf("only %d actions found", n)
	}
	if !regexp.MustCompile(`(?m)^\s+version: v\d+\.\d+\.\d+$`).MatchString(readRepoFile(t, ".github/workflows/release.yml")) {
		t.Error("the goreleaser version is not pinned exactly")
	}
}

// Spec scenario "Verify checksums" (distribution): goreleaser builds the
// darwin arm64, amd64 and universal archives with checksums.txt and signs it
// keyless into checksums.txt.sig and .pem plus a sigstore bundle, which the
// release uploads.
func TestVerifyChecksums(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	mustContain(t, ".goreleaser.yaml", gr,
		"version: 2",
		"CGO_ENABLED=1",
		"goarch: [arm64, amd64]",
		"universal_binaries:",
		"universal{{ else }}",
		"name_template: checksums.txt",
		"artifacts: checksum",
		`signature: "${artifact}.sig"`,
		`certificate: "${artifact}.pem"`,
		"--new-bundle-format",
		"--bundle=${artifact}.sigstore.json",
		"glob: ./dist/checksums.txt.sigstore.json",
		"codesign --force --sign -",
		"- LICENSE",
		"- README.md",
		"- docs/safety.md",
	)
}

// Spec scenario "Tagged build" (distribution): the release ldflags set
// main.version and main.commit, and kipple version prints what they set.
func TestTaggedBuild(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	mustContain(t, ".goreleaser.yaml", gr, "-X main.version={{ .Version }}", "-X main.commit={{ .FullCommit }}", "-X main.date=")
	saved := [3]string{version, commit, date}
	t.Cleanup(func() { version, commit, date = saved[0], saved[1], saved[2] })
	version, commit, date = "0.1.0", "0123456789abcdef0123456789abcdef01234567", "2026-11-20T00:00:00Z"
	code, out, _ := cli(t, "version")
	if code != exitOK || !strings.Contains(out, "kipple 0.1.0 (commit 0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("exit %d: %q", code, out)
	}
	code, out, _ = cli(t, "version", "--json")
	var doc versionDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != exitOK {
		t.Fatalf("exit %d %v", code, err)
	}
	if doc.Version != "0.1.0" || doc.Commit != commit || doc.Date != date {
		t.Fatalf("%+v", doc)
	}
}

// Should-fix 9: kipple version says whether Apple's Trash API fallback is
// built in (it needs cgo, so a go install without the Xcode command line
// tools leaves it out).
func TestVersionReportsTrashFallback(t *testing.T) {
	code, out, _ := cli(t, "version")
	want := "Trash fallback (Apple's Trash API): built in"
	if !trash.FinderAvailable {
		want = "Trash fallback (Apple's Trash API): not built in"
	}
	if code != exitOK || !strings.Contains(out, want) {
		t.Fatalf("exit %d: %q", code, out)
	}
	_, out, _ = cli(t, "version", "--json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc["trash_fallback"] != trash.FinderAvailable {
		t.Fatalf("%v %v", doc, err)
	}
}

// Should-fix 9: exit code 3 (nothing to do) comes from clean --apply with
// nothing selected and from restore (dry run or not) with nothing in the
// receipt to put back; a clean dry run exits 0. The README says exactly
// this.
func TestNothingToDoExitCode(t *testing.T) {
	testutil.NewHome(t, testutil.Spec{Dirs: testutil.TrashDirs()})
	if code, _, _ := cli(t, "clean"); code != exitOK {
		t.Fatalf("clean dry run on an empty home: exit %d", code)
	}
	if code, _, _ := cli(t, "clean", "--apply"); code != exitNothing {
		t.Fatalf("clean --apply on an empty home: exit %d", code)
	}
	cleanFixture(t)
	_, out, _ := cli(t, "scan", "--json")
	var p plan.Plan
	exactlyOneJSON(t, out, &p)
	var info string
	for _, it := range p.Items {
		if it.Tier == "info" {
			info = it.ID
		}
	}
	code, out, _ := cli(t, "clean", "--apply", "--json", "--only", info)
	var res plan.Result
	exactlyOneJSON(t, out, &res)
	if code != exitPartial || res.ReceiptID == "" {
		t.Fatalf("clean of an info item: exit %d %+v", code, res)
	}
	for _, args := range [][]string{{"restore", res.ReceiptID}, {"restore", res.ReceiptID, "--apply"}} {
		if code, _, _ := cli(t, args...); code != exitNothing {
			t.Errorf("%v with nothing to put back: exit %d", args, code)
		}
	}
}
