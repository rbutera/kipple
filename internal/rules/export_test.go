package rules

import "slices"

// AddForTest adds r to the shared catalogue (catalogue.go) with the Linux
// roots linuxRoots (a linuxOverrides entry in platforms.go), as a
// contributor does when adding a rule whose location differs per platform
// (CONTRIBUTING.md, "Adding a rule"). The returned function undoes it.
func AddForTest(r Rule, linuxRoots []string) (undo func()) {
	saved := catalogue
	catalogue = append(slices.Clone(catalogue), r)
	linuxOverrides[r.ID] = override{Roots: linuxRoots}
	return func() {
		catalogue = saved
		delete(linuxOverrides, r.ID)
	}
}
