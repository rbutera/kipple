//go:build gui

package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// kipple's own colour names, readable by widgets and RichText segments.
const (
	cSurface  fyne.ThemeColorName = "kipple.surface"
	cHeader   fyne.ThemeColorName = "kipple.header"
	cCursor   fyne.ThemeColorName = "kipple.cursor"
	cHover    fyne.ThemeColorName = "kipple.hover"
	cInk      fyne.ThemeColorName = "kipple.ink"
	cMuted    fyne.ThemeColorName = "kipple.muted"
	cAmber    fyne.ThemeColorName = "kipple.amber"
	cAmberInk fyne.ThemeColorName = "kipple.amberInk"
	cOnAmber  fyne.ThemeColorName = "kipple.onAmber"
	cControl  fyne.ThemeColorName = "kipple.control"
	cRecess   fyne.ThemeColorName = "kipple.recess"
	cScrim    fyne.ThemeColorName = "kipple.scrim"
	cPrimary  fyne.ThemeColorName = "kipple.primary"
	cOnPrim   fyne.ThemeColorName = "kipple.onPrimary"
	cSep      fyne.ThemeColorName = "kipple.separator"
	cGround   fyne.ThemeColorName = "kipple.background"
)

func hex(s string, a uint8) color.NRGBA {
	var v [3]uint8
	for i := 0; i < 3; i++ {
		var b uint8
		for _, c := range s[1+2*i : 3+2*i] {
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= uint8(c - '0')
			case c >= 'a' && c <= 'f':
				b |= uint8(c-'a') + 10
			case c >= 'A' && c <= 'F':
				b |= uint8(c-'A') + 10
			}
		}
		v[i] = b
	}
	return color.NRGBA{R: v[0], G: v[1], B: v[2], A: a}
}

// token is one colour in light and dark (docs/gui-design.md 8.1).
type token struct{ light, dark color.NRGBA }

var tokens = map[fyne.ThemeColorName]token{
	cGround:           {hex("#F3F4EF", 255), hex("#10221E", 255)},
	cSurface:          {hex("#FBFCF9", 255), hex("#152B26", 255)},
	cHeader:           {hex("#E9EDE8", 255), hex("#1A332D", 255)},
	cCursor:           {hex("#E3EBE4", 255), hex("#1F4038", 255)},
	cHover:            {hex("#EEF2EE", 255), hex("#19352F", 255)},
	cInk:              {hex("#10221E", 255), hex("#E3EBE4", 255)},
	cMuted:            {hex("#4A5C57", 255), hex("#9DB2AA", 255)},
	cAmber:            {hex("#C26A0C", 255), hex("#F2A43A", 255)},
	cAmberInk:         {hex("#995108", 255), hex("#F2A43A", 255)},
	cOnAmber:          {hex("#FBFCF9", 255), hex("#10221E", 255)},
	cPrimary:          {hex("#10221E", 255), hex("#E3EBE4", 255)},
	cOnPrim:           {hex("#F3F4EF", 255), hex("#10221E", 255)},
	cControl:          {hex("#4A5C57", 255), hex("#9DB2AA", 255)},
	cSep:              {hex("#D3DBD5", 255), hex("#2C4943", 255)},
	cRecess:           {hex("#E2E7E2", 255), hex("#0B1815", 255)},
	cScrim:            {hex("#10221E", 82), hex("#000000", 140)},
	"kipple.disabled": {hex("#8A9A94", 255), hex("#6B807A", 255)},
	"kipple.shadow":   {hex("#10221E", 41), hex("#000000", 115)},
}

func (t token) pick(v fyne.ThemeVariant) color.NRGBA {
	if v == theme.VariantDark {
		return t.dark
	}
	return t.light
}

// alpha returns c at fraction a of full opacity.
func alpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(float64(255) * a)
	return c
}

// fyneNames maps Fyne's colour names to tokens (docs/gui-design.md 8.2).
var fyneNames = map[fyne.ThemeColorName]fyne.ThemeColorName{
	theme.ColorNameBackground:          cGround,
	theme.ColorNameForeground:          cInk,
	theme.ColorNamePlaceHolder:         cMuted,
	theme.ColorNameDisabled:            "kipple.disabled",
	theme.ColorNamePrimary:             cPrimary,
	theme.ColorNameForegroundOnPrimary: cOnPrim,
	theme.ColorNameButton:              cCursor,
	theme.ColorNameDisabledButton:      cHeader,
	theme.ColorNameSelection:           cCursor,
	theme.ColorNameSeparator:           cSep,
	theme.ColorNameInputBackground:     cSurface,
	theme.ColorNameInputBorder:         cControl,
	theme.ColorNameHeaderBackground:    cHeader,
	theme.ColorNameMenuBackground:      cSurface,
	theme.ColorNameOverlayBackground:   cSurface,
	theme.ColorNameShadow:              "kipple.shadow",
	theme.ColorNameHyperlink:           cInk,
	// Errors, warnings and success are ink: amber means one thing only
	// (decision 1), and these states are carried by an icon and words.
	theme.ColorNameError:               cInk,
	theme.ColorNameWarning:             cInk,
	theme.ColorNameSuccess:             cInk,
	theme.ColorNameForegroundOnError:   cGround,
	theme.ColorNameForegroundOnWarning: cGround,
	theme.ColorNameForegroundOnSuccess: cGround,
}

// kippleTheme is the GUI's theme. It follows the system appearance unless
// variant is forced (the screenshot tests force light and dark).
type kippleTheme struct {
	fonts  systemFonts
	zoom   float32
	forced *fyne.ThemeVariant
}

func newTheme(f systemFonts) *kippleTheme { return &kippleTheme{fonts: f, zoom: 1} }

func (t *kippleTheme) variant(v fyne.ThemeVariant) fyne.ThemeVariant {
	if t.forced != nil {
		return *t.forced
	}
	return v
}

// Color implements fyne.Theme.
func (t *kippleTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	v = t.variant(v)
	if tok, ok := tokens[n]; ok {
		return tok.pick(v)
	}
	if m, ok := fyneNames[n]; ok {
		return tokens[m].pick(v)
	}
	ink := tokens[cInk].pick(v)
	switch n {
	case theme.ColorNameHover:
		return alpha(ink, 0.07)
	case theme.ColorNamePressed:
		return alpha(ink, 0.12)
	case theme.ColorNameFocus:
		return alpha(ink, 0.20)
	case theme.ColorNameScrollBar:
		return alpha(tokens[cMuted].pick(v), 0.55)
	case theme.ColorNameScrollBarBackground:
		return color.Transparent
	}
	return theme.DefaultTheme().Color(n, v)
}

// Font implements fyne.Theme: the system fonts, or Fyne's bundled ones.
func (t *kippleTheme) Font(s fyne.TextStyle) fyne.Resource {
	switch {
	case s.Monospace && t.fonts.mono != nil:
		return t.fonts.mono
	case s.Monospace:
		return theme.DefaultTheme().Font(s)
	case s.Bold && t.fonts.bold != nil:
		return t.fonts.bold
	case s.Bold:
		return theme.DefaultTheme().Font(s)
	case t.fonts.regular != nil && !s.Italic:
		return t.fonts.regular
	}
	return theme.DefaultTheme().Font(s)
}

// Icon implements fyne.Theme with Fyne's icons (recoloured with ink).
func (t *kippleTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

// Type and spacing (docs/gui-design.md 8.3, 8.4).
const (
	sizeCaption    = 11
	sizeBody       = 13
	sizeSubheading = 15
	sizeHeading    = 20
	sizeDisplay    = 28
)

var sizes = map[fyne.ThemeSizeName]float32{
	theme.SizeNameCaptionText:        sizeCaption,
	theme.SizeNameText:               sizeBody,
	theme.SizeNameSubHeadingText:     sizeSubheading,
	theme.SizeNameHeadingText:        sizeHeading,
	theme.SizeNameLineSpacing:        4,
	theme.SizeNamePadding:            4,
	theme.SizeNameInnerPadding:       8,
	theme.SizeNameInputBorder:        1,
	theme.SizeNameInputRadius:        4,
	theme.SizeNameSelectionRadius:    6,
	theme.SizeNameButtonRadius:       6,
	theme.SizeNameCardRadius:         10,
	theme.SizeNameDialogRadius:       12,
	theme.SizeNamePopupRadius:        8,
	theme.SizeNameMenuRadius:         6,
	theme.SizeNameSeparatorThickness: 1,
	theme.SizeNameScrollBar:          8,
	theme.SizeNameScrollBarSmall:     3,
	theme.SizeNameScrollBarRadius:    4,
	theme.SizeNameInlineIcon:         16,
	theme.SizeNameModalBlurRadius:    0,
}

// Size implements fyne.Theme, scaled by the session zoom.
func (t *kippleTheme) Size(n fyne.ThemeSizeName) float32 {
	if s, ok := sizes[n]; ok {
		return s * t.zoom
	}
	return theme.DefaultTheme().Size(n) * t.zoom
}

// Zoom limits (docs/gui-design.md 10.3).
const (
	zoomMin = 0.85
	zoomMax = 1.5
)

// setZoom clamps and sets the zoom factor.
func (t *kippleTheme) setZoom(z float32) {
	if z < zoomMin {
		z = zoomMin
	}
	if z > zoomMax {
		z = zoomMax
	}
	t.zoom = z
}

// col reads a theme colour for the current variant.
func col(n fyne.ThemeColorName) color.Color {
	return fyne.CurrentApp().Settings().Theme().Color(n, fyne.CurrentApp().Settings().ThemeVariant())
}

// isDark reports whether the current appearance is dark.
func isDark() bool {
	th, ok := fyne.CurrentApp().Settings().Theme().(*kippleTheme)
	v := fyne.CurrentApp().Settings().ThemeVariant()
	if ok {
		v = th.variant(v)
	}
	return v == theme.VariantDark
}

// sz is a theme size scaled by the zoom.
func sz(n float32) float32 {
	if th, ok := fyne.CurrentApp().Settings().Theme().(*kippleTheme); ok {
		return n * th.zoom
	}
	return n
}
