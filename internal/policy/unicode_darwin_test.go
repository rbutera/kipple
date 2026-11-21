//go:build darwin

// APFS looks names up case- and normalisation-insensitively, so an alias
// spelling (SAMPLE, ſample, NFC for NFD) opens another entry's object.
// Linux file systems compare bytes: there these spellings name nothing,
// and the checks below have no object to refuse. The lexical checks of the
// same aliases run everywhere (unicode_test.go).

package policy

import (
	"strings"
	"testing"
)

// The identity layer refuses a fold alias of a denylisted location on its
// own, wherever APFS resolves the alias to the real object.
func TestUnicodeAliasOfADenylistedFolderOnDisk(t *testing.T) {
	home := unicodeFixture(t)
	for _, tc := range unicodeAliasCases() {
		t.Run(tc.name, func(t *testing.T) {
			if !sameObject(home+"/"+tc.alias, home+"/"+tc.real) {
				t.Skipf("this volume does not resolve %q to %q (a case-sensitive APFS volume)", tc.alias, tc.real)
			}
			comps := strings.Split(tc.alias, "/")
			if got := checkFilesystem(home, comps, anyType, collectProtectedIDs(home, nil)); got.Reason != ReasonDenylisted {
				t.Fatalf("identity check alone: want %q, got %+v", ReasonDenylisted, got)
			}
		})
	}
}

// storedNameAlias is a TestStoredNameMismatch case whose spelling APFS
// resolves to another stored name.
type storedNameAlias struct {
	name string
	rule Rule
	rel  string
	real string
	want string
}

// storedNameAliasCases are the alias cases of TestStoredNameMismatch; they
// hold only where the volume aliases the spelling.
func storedNameAliasCases(caches, libGlob Rule) []storedNameAlias {
	return []storedNameAlias{
		{"Case variant of the item", caches, "Library/Caches/SAMPLE", "Library/Caches/sample", ReasonNameMismatch},
		{"Long s variant of the item", caches, "Library/Caches/ſample", "Library/Caches/sample", ReasonNameMismatch},
		{"NFC spelling of an NFD-stored item", caches, "Library/Caches/" + nfcCafe, "Library/Caches/" + nfdCafe, ReasonNameMismatch},
		{"NFD spelling of an NFC-stored item", caches, "Library/Caches/nfc-" + nfdCafe, "Library/Caches/nfc-" + nfcCafe, ReasonNameMismatch},
		{"Case variant in the middle of the path", libGlob, "Library/CACHES/sample/blob", "Library/Caches/sample/blob", ReasonNameMismatch},
	}
}

// Every component must be byte-identical to the name stored on disk, so a
// spelling APFS resolves to another entry is refused even when nothing it
// reaches is protected (the alias cases of TestStoredNameMismatch).
func TestStoredNameMismatchAliases(t *testing.T) {
	home := unicodeFixture(t)
	caches := StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}, Kind: MatchChildren}
	libGlob := StaticRule{ID: "x", Roots: []string{"Library/*/sample"}, Kind: MatchChildren}
	for _, tc := range storedNameAliasCases(caches, libGlob) {
		t.Run(tc.name, func(t *testing.T) {
			if !sameObject(home+"/"+tc.rel, home+"/"+tc.real) {
				t.Skip("this volume does not alias the spelling (a case-sensitive APFS volume)")
			}
			if got := authorizeBoth(t, Env{Home: home}, tc.rule, home+"/"+tc.rel); got.Reason != tc.want {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
		})
	}
}

// With the inode identity check switched off, the stored-name check alone
// refuses every alias spelling with name_mismatch: case, long s and both
// NFC/NFD directions, including aliases of a user-protected folder that the
// lexical protect check does not match.
func TestStoredNameWithoutIdentityCheck(t *testing.T) {
	home := unicodeFixture(t)
	orig := collectIDs
	t.Cleanup(func() { collectIDs = orig })
	collectIDs = func(string, []string) *protectedIDs {
		return &protectedIDs{objects: map[fileID]string{}, ancestors: map[fileID]string{}, stats: map[string]statResult{}}
	}
	caches := StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}, Kind: MatchChildren}
	cases := []struct {
		name    string
		protect string // home-relative, "" for none
		alias   string
		real    string
	}{
		{"Case variant", "", "Library/Caches/SAMPLE", "Library/Caches/sample"},
		{"Long s variant", "", "Library/Caches/ſample", "Library/Caches/sample"},
		{"NFC spelling of an NFD-stored folder", "", "Library/Caches/" + nfcCafe, "Library/Caches/" + nfdCafe},
		{"NFD spelling of an NFC-stored folder", "", "Library/Caches/nfc-" + nfdCafe, "Library/Caches/nfc-" + nfcCafe},
		{"Protected folder stored NFD, typed NFD, candidate NFC", "Library/Caches/" + nfdCafe, "Library/Caches/" + nfcCafe, "Library/Caches/" + nfdCafe},
		{"Protected folder stored NFC, typed NFC, candidate NFD", "Library/Caches/nfc-" + nfcCafe, "Library/Caches/nfc-" + nfdCafe, "Library/Caches/nfc-" + nfcCafe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !sameObject(home+"/"+tc.alias, home+"/"+tc.real) {
				t.Skip("this volume does not alias the spelling")
			}
			env := Env{Home: home}
			if tc.protect != "" {
				env.Protect = []string{home + "/" + tc.protect}
			}
			if got := authorizeBoth(t, env, caches, home+"/"+tc.alias); got.Reason != ReasonNameMismatch {
				t.Fatalf("want %q, got %+v", ReasonNameMismatch, got)
			}
			// The stored spelling itself is still decided normally.
			want := ""
			if tc.protect != "" {
				want = ReasonUserProtected
			}
			if got := authorizeBoth(t, env, caches, home+"/"+tc.real); got.Reason != want {
				t.Fatalf("stored spelling: want %q, got %+v", want, got)
			}
		})
	}
}

// NFC and NFD spellings name the same directory on APFS. Folding does not
// normalise, so the identity check is what refuses the other spelling.
func TestNFCAndNFDSpellings(t *testing.T) {
	home := unicodeFixture(t)
	caches := StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}, Kind: MatchChildren}
	if !sameObject(home+"/Library/Caches/"+nfcCafe, home+"/Library/Caches/"+nfdCafe) {
		t.Skip("this volume is normalisation-sensitive")
	}
	cases := []struct {
		name, protect, candidate string
	}{
		{"Protect NFC, fixture and candidate NFD", nfcCafe, nfdCafe},
		{"Protect NFC, candidate NFC on an NFD fixture dir", nfcCafe, nfcCafe},
		{"Protect NFD, candidate NFC", nfdCafe, nfcCafe},
		{"Protect NFC, candidate is a file inside the NFD dir", nfcCafe, nfdCafe + "/blob"},
		{"Protect NFD, fixture and candidate NFC", "nfc-" + nfdCafe, "nfc-" + nfcCafe},
		{"Protect NFD, candidate NFD on an NFC fixture dir", "nfc-" + nfdCafe, "nfc-" + nfdCafe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := Env{Home: home, Protect: []string{home + "/Library/Caches/" + tc.protect}}
			rule := caches
			if strings.Contains(tc.candidate, "/") {
				rule = StaticRule{ID: "x", Roots: []string{"Library/Caches/" + strings.Split(tc.candidate, "/")[0]}, Kind: MatchChildren}
			}
			if got := authorizeBoth(t, env, rule, home+"/Library/Caches/"+tc.candidate); got.Reason != ReasonUserProtected {
				t.Fatalf("want %q, got %+v", ReasonUserProtected, got)
			}
		})
	}
}
