package main

import (
	"regexp"
	"strings"
	"testing"
)

// goreleaserBuild returns the builds entry with the given id from
// .goreleaser.yaml, up to the next entry or top-level key.
func goreleaserBuild(t *testing.T, gr, id string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\n  - id: ` + regexp.QuoteMeta(id) + `\n(.*?)(?:\n  - id: |\n[a-z_]+:)`).FindStringSubmatch(gr)
	if m == nil {
		t.Fatalf(".goreleaser.yaml has no build %q", id)
	}
	return m[1]
}

// Spec scenario "Linux archives in the signed checksums" (distribution),
// configuration side: a cgo-free linux amd64 and arm64 build with the
// release ldflags goes into the same CLI archives, and so into the one
// checksums.txt that is signed. make release-snapshot and make
// test-linux-tarball check the built files.
func TestLinuxArchivesInTheSignedChecksums(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	b := goreleaserBuild(t, gr, "kipple-linux")
	for _, want := range []string{
		"main: ./cmd/kipple", "binary: kipple", "- CGO_ENABLED=0", "goos: [linux]", "goarch: [amd64, arm64]", "- -trimpath",
		"-X main.version={{ .Version }} -X main.commit={{ .FullCommit }} -X main.date={{ .CommitDate }}",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("build kipple-linux lacks %q", want)
		}
	}
	if strings.Contains(b, "codesign") || strings.Contains(b, "tags:") {
		t.Error("the Linux build must not codesign or use build tags")
	}
	mustContain(t, ".goreleaser.yaml", gr, "ids: [kipple, kipple-universal, kipple-linux]", "name_template: checksums.txt", "artifacts: checksum")
	if n := strings.Count(gr, "\nchecksum:"); n != 1 {
		t.Errorf("want one checksum section, found %d", n)
	}
}

// Spec scenario "No Linux GUI artifact" (gui): the GUI is built for macOS
// only, and nothing names a Linux GUI file.
func TestNoLinuxGUIArtifact(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	if b := goreleaserBuild(t, gr, "kipple-gui"); !strings.Contains(b, "goos: [darwin]") || strings.Contains(b, "linux") {
		t.Fatalf("the kipple-gui build is not macOS-only:\n%s", b)
	}
	if strings.Contains(gr, "kipple-gui_{{ .Version }}_linux") || strings.Contains(gr, "kipple-gui_*_linux") {
		t.Fatal(".goreleaser.yaml names a Linux GUI artifact")
	}
}

// Spec scenarios "Linux failure blocks release" and "Runner home
// unchanged" (distribution), workflow side, one subtest each.
func TestLinuxCIJob(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	i := strings.Index(ci, "\n  linux:\n")
	if i < 0 {
		t.Fatal("ci.yml has no linux job")
	}
	job := ci[i:]

	// The Ubuntu job is part of the CI workflow whose success the release
	// gate requires, so its failure stops a release.
	t.Run("Linux failure blocks release", func(t *testing.T) {
		mustContain(t, "ci.yml linux job", job,
			"runs-on: ubuntu-latest", "timeout-minutes:", "make fmt-check", "GOOS: linux", "go vet ./...", "make check-no-real-home",
			"go test -race -count=1 ./...", "go build ./...", `CGO_ENABLED: "0"`, "scripts/agent-flow-check.sh",
			"make test-linux-mounts", "go build -tags gui", "-run 'TestLinuxRefusal'",
		)
		rel := readRepoFile(t, ".github/workflows/release.yml")
		mustContain(t, "release.yml", rel, "actions/workflows/ci.yml/runs", `.conclusion == "success"`, "needs: ci-green")
	})

	// Every test step runs between the two listings of the runner home,
	// the check is the job's last step, and nothing that writes under home
	// by design (sudo apt) runs between them.
	t.Run("Runner home unchanged", func(t *testing.T) {
		mustContain(t, "ci.yml linux job", job, "GOCACHE=${RUNNER_TEMP}", "GOMODCACHE=${RUNNER_TEMP}", "go telemetry off",
			`-path "$HOME/runners/*/_diag"`)
		if strings.Contains(job, "-name _diag") {
			t.Error("the listings prune every _diag folder, not only the runner's")
		}
		snap := strings.Index(job, "- name: Snapshot the runner home")
		check := strings.Index(job, "- name: Runner home unchanged")
		if snap < 0 || check < snap {
			t.Fatal("the job lacks the two home listings in order")
		}
		for _, step := range []string{"go test -race -count=1 ./...", "scripts/agent-flow-check.sh", "make test-linux-mounts", "go build -tags gui", "-run 'TestLinuxRefusal'"} {
			if k := strings.Index(job, step); k < snap || k > check {
				t.Errorf("%q does not run between the two home listings", step)
			}
		}
		if k := strings.Index(job, "sudo apt-get"); k < 0 || k > snap {
			t.Error("the GUI packages are not installed before the first home listing")
		}
		if strings.Contains(job[check+len("- name: Runner home unchanged"):], "- name:") || strings.Contains(job[check:], "- uses:") {
			t.Error("Runner home unchanged is not the job's last step")
		}
	})
}

// Task 3.7: the v0.3.0 release notes cover what the task lists: the Linux
// CLI and TUI, the larger denylist, os and arch in version, mover
// freedesktop, and the GUI still macOS-only.
func TestReleaseNotesV030(t *testing.T) {
	notes := readRepoFile(t, "docs/release-notes/v0.3.0.md")
	mustContain(t, "docs/release-notes/v0.3.0.md", notes,
		"runs on Linux", "`kipple tui`", "A larger denylist", "`os` and `arch`", `"mover": "freedesktop"`,
		"The Mac app stays macOS-only", "`mounts_unavailable`", "`unsupported_platform`", "kipple_0.3.0_linux_amd64.tar.gz")
	// The release uses docs/release-notes/<tag>.md as its body and fails
	// before building anything when the file is missing.
	rel := readRepoFile(t, ".github/workflows/release.yml")
	mustContain(t, "release.yml", rel,
		"args: release --clean --release-notes docs/release-notes/${{ github.ref_name }}.md",
		"NOTES: docs/release-notes/${{ github.ref_name }}.md", `if [ ! -s "$NOTES" ]; then`)
	if strings.Index(rel, `if [ ! -s "$NOTES" ]`) > strings.Index(rel, "goreleaser/goreleaser-action@") {
		t.Error("the release-notes check does not run before goreleaser")
	}
}
