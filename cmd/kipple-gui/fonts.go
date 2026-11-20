//go:build gui

package main

import (
	"encoding/binary"
	"errors"
	"os"

	"fyne.io/fyne/v2"
)

// System font files. brand.md: interface text is the platform system font.
// Fyne cannot ask macOS for it, so the theme reads the installed files at
// run time; no font file ships with kipple.
//
// The font spike (spike in 5.1, see design.md D13) found that Fyne renders
// SF Pro (SFNS.ttf) and SF Mono, but both are variable fonts and Fyne
// renders only their default instance: SF's "bold" comes out at regular
// weight. Bold text therefore uses Helvetica Neue Bold, the system's static
// bold grotesque, taken out of HelveticaNeue.ttc. Anything that cannot be
// read or parsed falls back to Fyne's bundled font for that style.
const (
	sfText    = "/System/Library/Fonts/SFNS.ttf"
	sfMono    = "/System/Library/Fonts/SFNSMono.ttf"
	boldColl  = "/System/Library/Fonts/HelveticaNeue.ttc"
	boldClass = 700
)

// systemFonts are the loaded faces; a nil field means Fyne's bundled font.
type systemFonts struct {
	regular, bold, mono fyne.Resource
}

// loadSystemFonts reads the system fonts. read is os.ReadFile when nil.
func loadSystemFonts(read func(string) ([]byte, error)) systemFonts {
	if read == nil {
		read = os.ReadFile
	}
	var f systemFonts
	if b, err := read(sfText); err == nil && isSFNT(b) {
		f.regular = fyne.NewStaticResource("SFNS.ttf", b)
	}
	if b, err := read(sfMono); err == nil && isSFNT(b) {
		f.mono = fyne.NewStaticResource("SFNSMono.ttf", b)
	}
	if b, err := read(boldColl); err == nil {
		if face, err := faceFromCollection(b, boldClass); err == nil {
			f.bold = fyne.NewStaticResource("HelveticaNeue-Bold.ttf", face)
		}
	}
	return f
}

// isSFNT reports whether b starts like a single TrueType or OpenType font.
func isSFNT(b []byte) bool {
	if len(b) < 12 {
		return false
	}
	v := binary.BigEndian.Uint32(b)
	return v == 0x00010000 || v == 0x4F54544F /* OTTO */ || v == 0x74727565 /* true */
}

var errNoFace = errors.New("no matching face in the collection")

// faceFromCollection returns a standalone font file for the first upright
// face of TrueType collection b whose OS/2 weight class is weight. Fyne
// parses single fonts only, so the face's tables are copied out with a new
// table directory.
func faceFromCollection(b []byte, weight uint16) ([]byte, error) {
	be := binary.BigEndian
	if len(b) < 12 || string(b[:4]) != "ttcf" {
		return nil, errors.New("not a font collection")
	}
	n := int(be.Uint32(b[8:]))
	if n <= 0 || n > 256 || len(b) < 12+4*n {
		return nil, errors.New("bad font collection header")
	}
	for i := 0; i < n; i++ {
		off := int(be.Uint32(b[12+4*i:]))
		tables, err := tableDir(b, off)
		if err != nil {
			continue
		}
		os2, ok := tables["OS/2"]
		if !ok || os2.length < 64 {
			continue
		}
		t := b[os2.offset : os2.offset+os2.length]
		if be.Uint16(t[4:]) != weight || be.Uint16(t[62:])&1 != 0 { // usWeightClass, fsSelection italic bit
			continue
		}
		return standalone(b, off, tables)
	}
	return nil, errNoFace
}

type table struct {
	tag            string
	checksum       uint32
	offset, length int
}

// tableDir reads the table directory at off.
func tableDir(b []byte, off int) (map[string]table, error) {
	be := binary.BigEndian
	if off < 0 || off+12 > len(b) {
		return nil, errors.New("bad offset")
	}
	num := int(be.Uint16(b[off+4:]))
	if off+12+16*num > len(b) {
		return nil, errors.New("bad table directory")
	}
	out := map[string]table{}
	for j := 0; j < num; j++ {
		r := b[off+12+16*j:]
		t := table{tag: string(r[:4]), checksum: be.Uint32(r[4:]), offset: int(be.Uint32(r[8:])), length: int(be.Uint32(r[12:]))}
		if t.offset < 0 || t.length < 0 || t.offset+t.length > len(b) {
			return nil, errors.New("table out of range")
		}
		out[t.tag] = t
	}
	return out, nil
}

// standalone writes the face at off as a single font file: the same
// header and tables, each table copied after the directory, 4-byte
// aligned, with its offset rewritten.
func standalone(b []byte, off int, tables map[string]table) ([]byte, error) {
	be := binary.BigEndian
	num := int(be.Uint16(b[off+4:]))
	head := 12 + 16*num
	out := make([]byte, head, head+len(b)/4)
	copy(out, b[off:off+12])
	pos := head
	for j := 0; j < num; j++ {
		r := b[off+12+16*j:]
		t := tables[string(r[:4])]
		rec := out[12+16*j:]
		copy(rec[:4], r[:4])
		be.PutUint32(rec[4:], t.checksum)
		be.PutUint32(rec[8:], uint32(pos))
		be.PutUint32(rec[12:], uint32(t.length))
		out = append(out, b[t.offset:t.offset+t.length]...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		pos = len(out)
	}
	return out, nil
}
