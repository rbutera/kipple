package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
		`.conclusion == "success"`,
		"exit 1",
		"needs: ci-green",
		"id-token: write",
		"contents: write",
		"sigstore/cosign-installer",
		"goreleaser/goreleaser-action",
	)
	if strings.Index(wf, "ci-green:") > strings.Index(wf, "release:\n") {
		t.Error("the CI check is not the first job")
	}
}

// Spec scenario "Verify checksums" (distribution): goreleaser builds the
// darwin arm64, amd64 and universal archives with checksums.txt and signs it
// keyless into checksums.txt.sig and .pem.
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
