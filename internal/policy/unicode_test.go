package policy

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

const (
	nfcCafe = "café"  // é as one code point
	nfdCafe = "café" // e followed by a combining acute accent
)

// unicodeFixture holds real denylisted objects. On APFS (case-insensitive,
// normalisation-insensitive) the alias spellings below open these same files.
func unicodeFixture(t *testing.T) string {
	t.Helper()
	return testutil.NewHome(t, testutil.Spec{
		Files: []testutil.File{
			{Path: "Documents/taxes.pdf", Content: "x"},
			{Path: ".ssh/id_ed25519", Content: "x"},
			{Path: "Library/Messages/chat.db", Content: "x"},
			{Path: "Library/CloudStorage/x", Content: "x"},
			{Path: "Library/Keychains/login.db", Content: "x"},
			{Path: "Library/Caches/" + nfdCafe + "/blob", Content: "x"},
			{Path: "Library/Caches/plain/blob", Content: "x"},
			{Path: "Library/Caches/sample/blob", Content: "x"},
			{Path: "Library/Caches/nfc-" + nfcCafe + "/blob", Content: "x"},
		},
	})
}

// sameObject reports whether two spellings resolve to one object on this
// filesystem, so a test can skip alias cases on a case-sensitive volume.
func sameObject(a, b string) bool {
	fa, errA := lstat(a)
	fb, errB := lstat(b)
	if errA != nil || errB != nil {
		return false
	}
	ia, okA := identityOf(fa)
	ib, okB := identityOf(fb)
	return okA && okB && ia == ib
}

// Unicode case-fold aliases of denylisted locations are refused, whether the
// rule root itself is spelled with the alias or a glob covers it.
func TestUnicodeFoldAliasesRefused(t *testing.T) {
	home := unicodeFixture(t)
	star := StaticRule{ID: "hypothetical.star", Roots: []string{"*"}}
	cases := []struct {
		name  string
		rule  Rule
		alias string
		real  string
	}{
		{"Long s in Documents", StaticRule{ID: "x", Roots: []string{"Documentſ"}}, "Documentſ/taxes.pdf", "Documents/taxes.pdf"},
		{"Long s in Documents under a glob root", star, "Documentſ/taxes.pdf", "Documents/taxes.pdf"},
		{"Long s in .ssh", StaticRule{ID: "x", Roots: []string{".ſſh"}}, ".ſſh/id_ed25519", ".ssh/id_ed25519"},
		{"Long s in Library/Messages", StaticRule{ID: "x", Roots: []string{"Library/Meſſages"}}, "Library/Meſſages/chat.db", "Library/Messages/chat.db"},
		{"St ligature in Library/CloudStorage", StaticRule{ID: "x", Roots: []string{"Library/Cloudﬆorage"}}, "Library/Cloudﬆorage/x", "Library/CloudStorage/x"},
		{"Long st ligature in Library/CloudStorage", StaticRule{ID: "x", Roots: []string{"Library/Cloudﬅorage"}}, "Library/Cloudﬅorage/x", "Library/CloudStorage/x"},
		{"Sharp s in Library/Messages", StaticRule{ID: "x", Roots: []string{"Library/Meßages"}}, "Library/Meßages/chat.db", "Library/Messages/chat.db"},
		{"Capital sharp s in Library/Messages", StaticRule{ID: "x", Roots: []string{"Library/MEẞAGES"}}, "Library/MEẞAGES/chat.db", "Library/Messages/chat.db"},
		{"Kelvin sign in Library/Keychains", StaticRule{ID: "x", Roots: []string{"Library/Keychains"}}, "Library/Keychains/login.db", "Library/Keychains/login.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Authorize(Env{Home: home}, tc.rule, home+"/"+tc.alias); got.Reason != ReasonDenylisted {
				t.Fatalf("want %q, got %+v", ReasonDenylisted, got)
			}
			if !IsDenylisted(tc.alias) {
				t.Fatalf("IsDenylisted(%q) = false", tc.alias)
			}
			// The identity layer refuses it on its own too, wherever the
			// filesystem resolves the alias to the real object.
			if !sameObject(home+"/"+tc.alias, home+"/"+tc.real) {
				t.Skipf("this volume does not resolve %q to %q", tc.alias, tc.real)
			}
			comps := strings.Split(tc.alias, "/")
			if got := checkFilesystem(home, comps, anyType, collectProtectedIDs(home, nil)); got.Reason != ReasonDenylisted {
				t.Fatalf("identity check alone: want %q, got %+v", ReasonDenylisted, got)
			}
		})
	}
}

// A case or fold alias of an ancestor of a denylisted location is refused by
// identity as well as lexically.
func TestIdentityRefusesAncestorAlias(t *testing.T) {
	home := unicodeFixture(t)
	for alias, real := range map[string]string{"LIBRARY": "Library", "Library/MESSAGES": "Library/Messages", ".SSH": ".ssh"} {
		if !sameObject(home+"/"+alias, home+"/"+real) {
			continue // not an alias on this volume
		}
		got := checkFilesystem(home, strings.Split(alias, "/"), anyType, collectProtectedIDs(home, nil))
		if got.Reason != ReasonDenylisted {
			t.Errorf("%s: want %q from the identity check, got %+v", alias, ReasonDenylisted, got)
		}
	}
	// A sibling that is not protected passes the identity check.
	if got := checkFilesystem(home, []string{"Library", "Caches", "plain"}, anyType, collectProtectedIDs(home, nil)); !got.Allowed {
		t.Errorf("unprotected sibling refused: %+v", got)
	}
}

// NFC and NFD spellings name the same directory on APFS. Folding does not
// normalise, so the identity check is what refuses the other spelling.
func TestNFCAndNFDSpellings(t *testing.T) {
	home := unicodeFixture(t)
	caches := StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}}
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
				rule = StaticRule{ID: "x", Roots: []string{"Library/Caches/" + strings.Split(tc.candidate, "/")[0]}}
			}
			if got := Authorize(env, rule, home+"/Library/Caches/"+tc.candidate); got.Reason != ReasonUserProtected {
				t.Fatalf("want %q, got %+v", ReasonUserProtected, got)
			}
		})
	}
}

func TestConfusableRune(t *testing.T) {
	bad := map[string]rune{
		"Documentſ":           'ſ',
		"Library/Meßages":     'ß',
		"Library/MEẞAGES":     'ẞ',
		"Library/Cloudﬆorage": 'ﬆ',
		"ﬀ":                   'ﬀ',
		"Kube":                'K',
		"bad\xffutf8":         '�',
	}
	for s, want := range bad {
		if r, ok := ConfusableRune(s); !ok || r != want {
			t.Errorf("ConfusableRune(%q) = %U, %v; want %U", s, r, ok, want)
		}
		if _, ok := ParseRoot(s); ok {
			t.Errorf("ParseRoot(%q) accepted a confusable root", s)
		}
	}
	for _, s := range []string{"Library/Caches", nfcCafe, nfdCafe, "Ünïcødé", "日本語", "Documents"} {
		if r, ok := ConfusableRune(s); ok {
			t.Errorf("ConfusableRune(%q) flagged %U", s, r)
		}
	}
	// Folding agrees with APFS for the aliases above.
	for alias, real := range map[string]string{
		"Documentſ": "documents", "Meßages": "messages", "Cloudﬆorage": "cloudstorage",
		"Keychains": "keychains", "MEẞAGES": "messages", "ﬁle": "file",
	} {
		if got := foldName(alias); got != real {
			t.Errorf("foldName(%q) = %q, want %q", alias, got, real)
		}
	}
}

// Every component must be byte-identical to the name stored on disk, so a
// spelling APFS would resolve to another entry is refused even when nothing
// it reaches is protected.
func TestStoredNameMismatch(t *testing.T) {
	home := unicodeFixture(t)
	caches := StaticRule{ID: "caches.user", Roots: []string{"Library/Caches"}}
	libGlob := StaticRule{ID: "x", Roots: []string{"Library/*/sample"}}
	cases := []struct {
		name string
		rule Rule
		rel  string
		real string // the stored spelling the alias resolves to
		want string
	}{
		{"Stored spelling is allowed", caches, "Library/Caches/sample", "", ""},
		{"Stored NFD spelling is allowed", caches, "Library/Caches/" + nfdCafe, "", ""},
		{"Stored NFC spelling is allowed", caches, "Library/Caches/nfc-" + nfcCafe, "", ""},
		{"Case variant of the item", caches, "Library/Caches/SAMPLE", "Library/Caches/sample", ReasonNameMismatch},
		{"Long s variant of the item", caches, "Library/Caches/ſample", "Library/Caches/sample", ReasonNameMismatch},
		{"NFC spelling of an NFD-stored item", caches, "Library/Caches/" + nfcCafe, "Library/Caches/" + nfdCafe, ReasonNameMismatch},
		{"NFD spelling of an NFC-stored item", caches, "Library/Caches/nfc-" + nfdCafe, "Library/Caches/nfc-" + nfcCafe, ReasonNameMismatch},
		{"Case variant in the middle of the path", libGlob, "Library/CACHES/sample/blob", "Library/Caches/sample/blob", ReasonNameMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.real != "" && !sameObject(home+"/"+tc.rel, home+"/"+tc.real) {
				t.Skip("this volume does not alias the spelling")
			}
			got := Authorize(Env{Home: home}, tc.rule, home+"/"+tc.rel)
			if tc.want == "" {
				if !got.Allowed {
					t.Fatalf("want allowed, got %+v", got)
				}
				return
			}
			if got.Reason != tc.want {
				t.Fatalf("want %q, got %+v", tc.want, got)
			}
		})
	}

	// A parent that cannot be listed cannot be verified.
	orig := hasStored
	t.Cleanup(func() { hasStored = orig })
	hasStored = func(string, string) (bool, error) { return false, fs.ErrPermission }
	if got := Authorize(Env{Home: home}, caches, home+"/Library/Caches/sample"); got.Reason != ReasonMissing {
		t.Fatalf("unlistable parent: want %q, got %+v", ReasonMissing, got)
	}
}
