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
	if strings.Count(gr, "- MACOSX_DEPLOYMENT_TARGET=11.0") != 2 {
		t.Error("both builds must set MACOSX_DEPLOYMENT_TARGET=11.0")
	}
	if strings.Count(gr, "glob: ./dist/kipple-gui_*_darwin_universal.app.zip") != 2 {
		t.Error("the .app zip is not in both checksum.extra_files and release.extra_files")
	}
	if !strings.Contains(gr, "ids: [kipple, kipple-universal, kipple-linux]") {
		t.Error("the CLI archives no longer name exactly the CLI builds")
	}
	app := readRepoFile(t, "scripts/build-app.sh")
	mustContain(t, "scripts/build-app.sh", app,
		"codesign --force --deep --sign -", "<string>com.rbutera.kipple</string>",
		"<key>CFBundleIconFile</key><string>kipple</string>", "assets/brand/kipple-icon.icns", "ditto -c -k",
		`min_macos="11.0"`, "vtool -show-build", "<key>LSMinimumSystemVersion</key><string>${min_macos}</string>")
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	mustContain(t, "ci.yml", ci,
		"go vet -tags gui ./cmd/kipple-gui/...", "go build -tags gui ./...",
		"go test -race -count=1 -tags gui ./cmd/kipple-gui/...",
		"scripts/gui-screenshots.sh", "scripts/build-app.sh", "args: --build-tags gui",
		`MACOSX_DEPLOYMENT_TARGET: "11.0"`)
	readme := readRepoFile(t, "README.md")
	mustContain(t, "README.md", readme, "kipple-gui_<version>_darwin_universal.app.zip", "**not notarised**",
		"](docs/images/gui-review-light.png)", "not yet usable with VoiceOver")
}

// Task 5.4: the brief's screenshots are committed, and the README shows
// the light and dark review.
func TestGUIScreenshotCommitted(t *testing.T) {
	for _, name := range []string{"gui-review-light", "gui-review-dark", "gui-confirm-light", "gui-done-light"} {
		fi, err := os.Stat("../../docs/images/" + name + ".png")
		if err != nil || fi.Size() < 10<<10 {
			t.Errorf("docs/images/%s.png: %v", name, err)
		}
	}
	mustContain(t, "README.md", readRepoFile(t, "README.md"), "](docs/images/gui-review-dark.png)")
}
