package rules

import (
	"runtime"
	"slices"
	"sort"
)

// Platforms kipple has a catalogue for.
const (
	PlatformDarwin = "darwin"
	PlatformLinux  = "linux"
)

// Platforms returns every platform with a catalogue.
func Platforms() []string { return []string{PlatformDarwin, PlatformLinux} }

// PlatformName is how a platform is named in messages.
func PlatformName(goos string) string {
	switch goos {
	case PlatformDarwin:
		return "macOS"
	case PlatformLinux:
		return "Linux"
	}
	return goos
}

// The catalogue is one list (catalogue, in catalogue.go: the macOS rules,
// unchanged since v0.2.0) plus, for Linux (linux-port design L8):
//
//   - macOSOnly: rules that exist on macOS only;
//   - linuxOverrides: shared rules whose roots, excludes, extensions or
//     owner differ on Linux, under the same ID;
//   - linuxOnly: rules that exist on Linux only.
//
// Shared rules are written once; CatalogueFor resolves one platform's list.

// macOSOnly are rule IDs with no Linux counterpart.
var macOSOnly = map[string]bool{
	"caches.user": true, "logs.user": true, "xcode.deriveddata": true, "simulator.caches": true,
	"swiftpm.cache": true, "cocoapods.cache": true, "xcode.devicesupport": true,
	"xcode.archives": true, "simulator.runtimes": true,
}

// override replaces parts of a shared rule on one platform; nil and empty
// fields keep the shared value.
type override struct {
	Roots      []string
	Exclude    []string
	Extensions []string
	Owner      *Owner
	What       string
}

// editorCacheRootsLinux returns the four Chromium/Electron cache folders of
// VS Code and its forks under ~/.config/<App>.
func editorCacheRootsLinux(app string) []string {
	base := ".config/" + app + "/"
	return []string{base + "Cache", base + "CachedData", base + "Code Cache", base + "GPUCache"}
}

// linuxOverrides are the Linux differences of shared rules.
var linuxOverrides = map[string]override{
	"homebrew.cache": {Roots: []string{".cache/Homebrew"}},
	"pnpm.store":     {Roots: []string{".local/share/pnpm/store", ".cache/pnpm"}},
	"yarn.cache":     {Roots: []string{".cache/yarn"}},
	"pip.cache":      {Roots: []string{".cache/pip"}},
	"poetry.cache":   {Roots: []string{".cache/pypoetry/cache", ".cache/pypoetry/artifacts"}},
	"go.build-cache": {Roots: []string{".cache/go-build"}},
	"editor.cursor": {Roots: editorCacheRootsLinux("Cursor"),
		Owner: &Owner{Name: "Cursor", Processes: []string{"cursor"}}},
	"editor.vscode": {Roots: editorCacheRootsLinux("Code"),
		Owner: &Owner{Name: "Visual Studio Code", Processes: []string{"code"}}},
	"editor.windsurf": {Roots: editorCacheRootsLinux("Windsurf"),
		Owner: &Owner{Name: "Windsurf", Processes: []string{"windsurf"}}},
	"downloads.installers": {Extensions: []string{".deb", ".rpm", ".AppImage", ".iso", ".run"},
		What: "Installer packages and disk images (.deb, .rpm, .AppImage, .iso, .run) in your Downloads folder that are more than two weeks old."},
	"docker.raw": {Roots: []string{".docker/desktop/vms/0/data"},
		Owner: &Owner{Name: "Docker Desktop", Processes: []string{"com.docker.backend"}}},
}

// jetbrainsProcesses are the JetBrains IDEs' process names on Linux.
var jetbrainsProcesses = []string{"idea", "pycharm", "goland", "webstorm", "clion", "rustrover", "rider", "phpstorm", "datagrip", "rubymine"}

// linuxOnly are the rules that exist on Linux only.
var linuxOnly = []Rule{
	{
		ID:         "xdg.cache",
		Title:      "App caches",
		Tier:       TierSafe,
		Roots:      []string{".cache"},
		Exclude:    XDGCacheExcludePatterns(),
		Match:      children,
		Conditions: Conditions{MinAgeDays: 7},
		Owner:      Owner{Name: "Linux apps"},
		What:       "Folders that apps keep in your ~/.cache folder, which the XDG standard reserves for data apps can recreate, untouched for a week.",
		IfRemoved:  "Apps may start a little slower the first time while they rebuild or re-download what they need, and no documents or settings are lost.",
	},
	{
		ID:         "jetbrains.caches",
		Title:      "JetBrains IDE caches",
		Tier:       TierReview,
		Roots:      []string{".cache/JetBrains"},
		Match:      children,
		Conditions: Conditions{MinAgeDays: 30, SkipIfOwnerRunning: true},
		Owner:      Owner{Name: "JetBrains IDEs", Processes: jetbrainsProcesses},
		What:       "Indexes, caches and the Local History of one JetBrains IDE version (IntelliJ IDEA, PyCharm, GoLand and others), untouched for 30 days.",
		IfRemoved:  "The IDE re-indexes your projects the next time it opens them, and its Local History of past edits in that version is gone.",
	},
	{
		ID:           "maven.repository",
		PackageCache: true,
		Title:        "Maven local repository",
		Tier:         TierReview,
		Roots:        []string{".m2/repository"},
		Match:        children,
		Owner:        Owner{Name: "Maven"},
		What:         "Java libraries Maven and Gradle downloaded into the local repository, plus anything you installed there with mvn install.",
		IfRemoved:    "Builds download their dependencies again, and snapshot versions you only installed locally must be rebuilt from their source.",
	},
	{
		ID:        "flatpak.app-cache",
		Title:     "Flatpak app caches",
		Tier:      TierReview,
		Roots:     []string{".var/app/*/cache"},
		Match:     children,
		Owner:     Owner{Name: "Flatpak apps"},
		What:      "Cache folders that Flatpak apps keep inside their own data folder under ~/.var/app.",
		IfRemoved: "Each app rebuilds what it needs on its next start, and its documents and settings in the same folder are untouched.",
	},
	{
		ID:           "docker.rootless",
		Title:        "Rootless Docker data",
		Tier:         TierInfo,
		Roots:        []string{".local/share/docker"},
		Match:        children,
		Owner:        Owner{Name: "Docker (rootless)", Processes: []string{"dockerd"}},
		OwnerCommand: "docker system prune",
		What:         "The images, containers, volumes and build cache of rootless Docker.",
		IfRemoved:    "Images, containers and volumes would be lost or corrupted, so reclaim space with Docker's own prune commands instead.",
	},
	{
		ID:           "podman.storage",
		Title:        "Podman storage",
		Tier:         TierInfo,
		Roots:        []string{".local/share/containers/storage"},
		Match:        children,
		Owner:        Owner{Name: "Podman", Processes: []string{"podman"}},
		OwnerCommand: "podman system prune",
		What:         "The images, containers and volumes of rootless Podman.",
		IfRemoved:    "Images, containers and volumes would be lost or corrupted, so reclaim space with Podman's own prune command instead.",
	},
	{
		ID:           "flatpak.user",
		Title:        "Flatpak user installation",
		Tier:         TierInfo,
		Roots:        []string{".local/share/flatpak"},
		Match:        children,
		Owner:        Owner{Name: "Flatpak"},
		OwnerCommand: "flatpak uninstall --unused",
		What:         "Flatpak apps and runtimes installed for your user only.",
		IfRemoved:    "Installed apps would stop working, so remove apps and unused runtimes with Flatpak itself.",
	},
	{
		ID:           "snap.data",
		Title:        "Snap data",
		Tier:         TierInfo,
		Roots:        []string{"snap"},
		Match:        children,
		Owner:        Owner{Name: "Snap"},
		OwnerCommand: "snap list --all, then snap remove --purge <name>",
		What:         "The per-user data folders of snap packages, one per app and revision.",
		IfRemoved:    "Snap apps would lose their settings and files, so remove snaps and their old revisions with snap itself.",
	},
}

// tierOrder sorts a catalogue: safe, review, manual, info.
var tierOrder = map[Tier]int{TierSafe: 0, TierReview: 1, TierManual: 2, TierInfo: 3}

// CatalogueFor returns a deep copy of the catalogue of goos ("darwin" or
// "linux"), in a stable order; any other platform has no rules. The macOS
// catalogue is exactly the v0.2.0 one.
func CatalogueFor(goos string) []Rule {
	switch goos {
	case PlatformDarwin:
		out := make([]Rule, len(catalogue))
		for i, r := range catalogue {
			out[i] = r.clone()
		}
		return out
	case PlatformLinux:
		var out []Rule
		for _, r := range catalogue {
			if macOSOnly[r.ID] {
				continue
			}
			r = r.clone()
			if o, ok := linuxOverrides[r.ID]; ok {
				if o.Roots != nil {
					r.Roots = slices.Clone(o.Roots)
				}
				if o.Exclude != nil {
					r.Exclude = slices.Clone(o.Exclude)
				}
				if o.Extensions != nil {
					r.Match.Extensions = slices.Clone(o.Extensions)
				}
				if o.Owner != nil {
					r.Owner = Owner{Name: o.Owner.Name, BundleIDs: slices.Clone(o.Owner.BundleIDs), Processes: slices.Clone(o.Owner.Processes)}
				}
				if o.What != "" {
					r.What = o.What
				}
			}
			out = append(out, r)
		}
		for _, r := range linuxOnly {
			out = append(out, r.clone())
		}
		sort.SliceStable(out, func(i, j int) bool { return tierOrder[out[i].Tier] < tierOrder[out[j].Tier] })
		return out
	}
	return nil
}

// platform is the catalogue Catalogue and Lookup return: the running
// platform's. Only tests change it (UseCatalogueOf).
var platform = runtime.GOOS

// UseCatalogueOf makes Catalogue, Lookup and Platform use goos's
// catalogue. It exists for tests written against one platform's catalogue
// (the macOS fixtures of the mechanics tests), which a test package's
// TestMain calls so they run unchanged on any host; production code never
// calls it.
func UseCatalogueOf(goos string) { platform = goos }

// Platform is the platform whose catalogue Catalogue returns
// (runtime.GOOS unless a test chose another).
func Platform() string { return platform }

// Catalogue returns a deep copy of the running platform's catalogue.
func Catalogue() []Rule { return CatalogueFor(platform) }

// LookupFor returns the rule with the given ID in goos's catalogue.
func LookupFor(goos, id string) (Rule, bool) {
	for _, r := range CatalogueFor(goos) {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// PlatformsOf lists the platforms whose catalogue has the rule id.
func PlatformsOf(id string) []string {
	var out []string
	for _, goos := range Platforms() {
		if _, ok := LookupFor(goos, id); ok {
			out = append(out, goos)
		}
	}
	return out
}
