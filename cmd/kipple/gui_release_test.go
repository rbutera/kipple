package main

import (
	"os"
	"strings"
	"testing"
)

// Task 5.3: the GUI ships as its own ad-hoc signed .app zip, outside the
// CLI archives and inside the signed checksums; CI builds, tests and
// bundles it.
func TestGUIRelease(t *testing.T) {
	gr := readRepoFile(t, ".goreleaser.yaml")
	mustContain(t, ".goreleaser.yaml", gr,
		"id: kipple-gui\n", "main: ./cmd/kipple-gui", "tags: [gui]", "ids: [kipple-gui]",
		`scripts/build-app.sh "{{ .Path }}" "{{ .Version }}" "dist/kipple-gui_{{ .Version }}_darwin_universal.app.zip"`,
	)
	if strings.Count(gr, "glob: ./dist/kipple-gui_*_darwin_universal.app.zip") != 2 {
		t.Error("the .app zip is not in both checksum.extra_files and release.extra_files")
	}
	if !strings.Contains(gr, "ids: [kipple, kipple-universal]") {
		t.Error("the CLI archives no longer name exactly the CLI builds")
	}
	app := readRepoFile(t, "scripts/build-app.sh")
	mustContain(t, "scripts/build-app.sh", app,
		"codesign --force --deep --sign -", "<string>com.rbutera.kipple</string>",
		"<key>CFBundleIconFile</key><string>kipple</string>", "assets/brand/kipple-icon.icns", "ditto -c -k")
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	mustContain(t, "ci.yml", ci,
		"go vet -tags gui ./cmd/kipple-gui/...", "go build -tags gui ./...",
		"go test -race -count=1 -tags gui ./cmd/kipple-gui/...",
		"scripts/gui-screenshots.sh", "scripts/build-app.sh", "args: --build-tags gui")
	readme := readRepoFile(t, "README.md")
	mustContain(t, "README.md", readme, "kipple-gui_<version>_darwin_universal.app.zip", "**not notarised**",
		"](docs/images/gui-review-light.png)", "not yet usable with VoiceOver")
}

// Task 5.4: the README's screenshot is committed.
func TestGUIScreenshotCommitted(t *testing.T) {
	fi, err := os.Stat("../../docs/images/gui-review-light.png")
	if err != nil || fi.Size() < 10<<10 {
		t.Fatalf("docs/images/gui-review-light.png: %v", err)
	}
}
