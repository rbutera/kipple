package policy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// APFS compares names case-insensitively using Unicode case folding, which is
// wider than strings.ToLower: "Documentſ" (long s), "Meßages" (sharp s) and
// "Cloudﬆorage" (st ligature) all open the same directory as their ASCII
// spellings. Every refusing comparison in this package uses foldName so those
// aliases are refused too. Normalisation (NFC/NFD) aliases are covered by the
// identity check in the filesystem phase, not by folding.

// foldExpansions are the full case foldings that map one rune to an ASCII
// sequence. APFS applies them; unicode.SimpleFold does not.
var foldExpansions = map[rune]string{
	'ß': "ss", // ß
	'ẞ': "ss", // ẞ
	'ﬀ': "ff",
	'ﬁ': "fi",
	'ﬂ': "fl",
	'ﬃ': "ffi",
	'ﬄ': "ffl",
	'ﬅ': "st",
	'ﬆ': "st",
}

// normSingletons are runes whose canonical decomposition is a single ASCII
// rune, so APFS (normalisation-insensitive) opens them as that ASCII name.
// A probe of every rune on APFS found exactly four single runes aliasing
// ASCII: U+017F long s and U+212A Kelvin sign (handled by case folding) and
// these two normalisation singletons.
var normSingletons = map[rune]string{
	'\u037E': ";", // GREEK QUESTION MARK
	'\u1FEF': "`", // GREEK VARIA
}

// foldRune returns the canonical folded form of r: its normalisation
// singleton or full-folding expansion if it has one, otherwise the smallest lower-case form in its simple case
// folding orbit (so 's', 'S' and 'ſ' all fold to "s", and 'K', 'k' and the
// Kelvin sign all fold to "k").
func foldRune(r rune) string {
	if e, ok := normSingletons[r]; ok {
		return e
	}
	if e, ok := foldExpansions[r]; ok {
		return e
	}
	best := unicode.ToLower(r)
	for o := unicode.SimpleFold(r); o != r; o = unicode.SimpleFold(o) {
		if l := unicode.ToLower(o); l < best {
			best = l
		}
	}
	return string(best)
}

// foldName folds every rune of s with foldRune.
func foldName(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteString(foldRune(r))
	}
	return b.String()
}

// isConfusable reports whether r folds differently from strings.ToLower, or
// folds to ASCII without being ASCII. Such runes let a name alias a different
// ASCII name on APFS, so rule roots and config paths may not contain them.
func isConfusable(r rune) bool {
	if r < utf8.RuneSelf {
		return false
	}
	if _, ok := foldExpansions[r]; ok {
		return true
	}
	f := foldRune(r)
	if f != string(unicode.ToLower(r)) {
		return true
	}
	return len(f) == 1 && f[0] < utf8.RuneSelf
}

// ConfusableRune returns the first rune in s that APFS case folding maps to
// a different name than strings.ToLower would (for example U+017F long s,
// U+00DF sharp s, U+212A Kelvin sign, the U+FB00..U+FB06 ligatures, or the
// normalisation singletons U+037E and U+1FEF), and
// true; or 0 and false when s has none. Invalid UTF-8 is reported as
// utf8.RuneError. Rule roots and config paths containing one are rejected.
func ConfusableRune(s string) (rune, bool) {
	if !utf8.ValidString(s) {
		return utf8.RuneError, true
	}
	for _, r := range s {
		if isConfusable(r) {
			return r, true
		}
	}
	return 0, false
}
