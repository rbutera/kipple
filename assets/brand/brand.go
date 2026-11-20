// Package brand embeds kipple's brand assets for the GUI: the mark and the
// wordmark (light and dark) and the 1024-point app icon. It uses only the
// standard library and has no build tag; only cmd/kipple-gui imports it.
//
// The SVGs colour their tiles with currentColor, which not every SVG
// renderer resolves, so Mark and Wordmark return them with currentColor
// replaced by the colour the file declares on its root element.
package brand

import (
	_ "embed"
	"regexp"
	"strings"
)

var (
	//go:embed kipple-mark.svg
	markLight []byte
	//go:embed kipple-mark-dark.svg
	markDark []byte
	//go:embed kipple-wordmark.svg
	wordLight []byte
	//go:embed kipple-wordmark-dark.svg
	wordDark []byte
	//go:embed kipple-icon-1024.png
	icon []byte
)

// Mark returns the mark for a dark or light ground.
func Mark(dark bool) []byte {
	if dark {
		return resolve(markDark)
	}
	return resolve(markLight)
}

// Wordmark returns the wordmark lockup for a dark or light ground.
func Wordmark(dark bool) []byte {
	if dark {
		return resolve(wordDark)
	}
	return resolve(wordLight)
}

// Icon returns the 1024-point app icon (PNG).
func Icon() []byte { return icon }

var rootColor = regexp.MustCompile(`<svg[^>]*\scolor="(#[0-9A-Fa-f]{6})"`)

// resolve replaces currentColor with the root element's declared colour.
func resolve(svg []byte) []byte {
	m := rootColor.FindSubmatch(svg)
	if m == nil {
		return svg
	}
	return []byte(strings.ReplaceAll(string(svg), "currentColor", string(m[1])))
}
