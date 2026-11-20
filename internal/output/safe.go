package output

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// SafeText returns s with every character that could act on a terminal
// replaced by a visible escape, so a file name can never retitle a window,
// clear the screen, recolour text, start a new row or reorder what follows
// it. Replaced: C0 controls (including newline and tab), DEL, C1 controls,
// the line and paragraph separators U+2028 and U+2029, the bidirectional
// marks, embeddings, overrides and isolates (U+061C, U+200E, U+200F,
// U+202A to U+202E, U+2066 to U+2069), and bytes that are not valid UTF-8.
// They are written as \n, \t and \r, \xNN (other bytes and characters
// below U+0100) or \uNNNN.
// Everything kipple prints for people that comes from the file system, a
// plan file or a receipt goes through it.
func SafeText(s string) string {
	return escape(s, false)
}

// safeBlock is SafeText for a whole block of output: it keeps the newlines
// that separate rows and escapes every other control character. It is the
// last pass over everything this package writes; fields are already
// escaped with SafeText, so a name cannot add a row.
func safeBlock(s string) string {
	return escape(s, true)
}

// Unsafe reports whether r is a character SafeText replaces.
func Unsafe(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

func escape(s string, keepNewline bool) string {
	clean := true
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && n == 1) || (Unsafe(r) && (!keepNewline || r != '\n')) {
			clean = false
			break
		}
		i += n
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case keepNewline && r == '\n':
			b.WriteByte('\n')
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case Unsafe(r) && r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case Unsafe(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// writeSafe writes b's text through the final safeBlock pass.
func writeSafe(w io.Writer, b *strings.Builder) error {
	_, err := io.WriteString(w, safeBlock(b.String()))
	return err
}

// safeList applies SafeText to each string.
func safeList(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = SafeText(s)
	}
	return out
}
