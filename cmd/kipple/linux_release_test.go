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
// unchanged" (distribution), workflow side: the Ubuntu job is part of the
// CI workflow whose success the release gate requires, and it brackets the
// tests with a listing of the runner home that must not change.
func TestLinuxCIJob(t *testing.T) {
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	i := strings.Index(ci, "\n  linux:\n")
	if i < 0 {
		t.Fatal("ci.yml has no linux job")
	}
	job := ci[i:]
	mustContain(t, "ci.yml linux job", job,
		"runs-on: ubuntu-latest", "make fmt-check", "GOOS: linux", "go vet ./...", "make check-no-real-home",
		"go test -race -count=1 ./...", "go build ./...", `CGO_ENABLED: "0"`, "scripts/agent-flow-check.sh",
		"make test-linux-mounts", "GOCACHE=${RUNNER_TEMP}", "GOMODCACHE=${RUNNER_TEMP}", "go telemetry off",
		"name: Snapshot the runner home", "name: Runner home unchanged", "go build -tags gui", "-run 'TestLinuxRefusal'",
	)
	snap, check := strings.Index(job, "Snapshot the runner home"), strings.Index(job, "Runner home unchanged")
	for _, step := range []string{"go test -race -count=1 ./...", "scripts/agent-flow-check.sh", "make test-linux-mounts"} {
		if k := strings.Index(job, step); k < snap || k > check {
			t.Errorf("%q does not run between the two home listings", step)
		}
	}
	rel := readRepoFile(t, ".github/workflows/release.yml")
	mustContain(t, "release.yml", rel, "actions/workflows/ci.yml/runs", `.conclusion == "success"`, "needs: ci-green")
}
