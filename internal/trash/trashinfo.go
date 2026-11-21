package trash

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TrashInfo is the content of a freedesktop.org .trashinfo file (Trash
// specification 1.0): where a trashed item came from and when.
type TrashInfo struct {
	// Path is the item's original absolute path, as raw bytes (decoded).
	Path string
	// DeletionDate is when it was trashed, in local time with no zone.
	DeletionDate time.Time
}

// infoDateLayout is the spec's DeletionDate format: local time, no zone.
const infoDateLayout = "2006-01-02T15:04:05"

// maxInfoSize bounds what DecodeTrashInfo accepts; a real info file is a
// few hundred bytes.
const maxInfoSize = 64 << 10

// EncodeTrashInfo returns the .trashinfo file for an item that was at path
// (absolute) and trashed at deleted. Path is percent-encoded per RFC 3986:
// every byte outside the unreserved set and "/" is written as %XX, so any
// byte sequence a Linux name can hold (space, "%", newline, invalid UTF-8)
// round-trips through DecodeTrashInfo. DeletionDate is deleted in local
// time, YYYY-MM-DDThh:mm:ss.
func EncodeTrashInfo(path string, deleted time.Time) ([]byte, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("trash: info path %q is not absolute", path)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, errors.New("trash: info path holds a NUL byte")
	}
	var b bytes.Buffer
	b.WriteString("[Trash Info]\nPath=")
	b.WriteString(escapePath(path))
	b.WriteString("\nDeletionDate=")
	b.WriteString(deleted.In(time.Local).Format(infoDateLayout))
	b.WriteString("\n")
	return b.Bytes(), nil
}

// DecodeTrashInfo parses a .trashinfo file strictly, as written for the
// home Trash: the first line that is not blank or a comment must be
// "[Trash Info]"; Path and DeletionDate must each appear exactly once in
// that group, Path must be absolute after decoding and every %XX escape
// valid, and DeletionDate must have the spec's format. Keys of other groups
// that follow are ignored, unknown keys in the group too. Anything else is
// an error.
func DecodeTrashInfo(data []byte) (TrashInfo, error) {
	if len(data) > maxInfoSize {
		return TrashInfo{}, errors.New("trash: info file too large")
	}
	var info TrashInfo
	seen := map[string]bool{}
	inGroup, sawGroup := false, false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !sawGroup {
				if line != "[Trash Info]" {
					return TrashInfo{}, fmt.Errorf("trash: info file starts with %q, not [Trash Info]", line)
				}
				sawGroup, inGroup = true, true
				continue
			}
			if line == "[Trash Info]" {
				return TrashInfo{}, errors.New("trash: info file repeats the [Trash Info] group")
			}
			inGroup = false
			continue
		}
		if !sawGroup {
			return TrashInfo{}, errors.New("trash: info file has a key before [Trash Info]")
		}
		if !inGroup {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return TrashInfo{}, fmt.Errorf("trash: info file line %q is not key=value", line)
		}
		key = strings.TrimSpace(key)
		if key != "Path" && key != "DeletionDate" {
			continue
		}
		if seen[key] {
			return TrashInfo{}, fmt.Errorf("trash: info file repeats %s", key)
		}
		seen[key] = true
		switch key {
		case "Path":
			p, err := unescapePath(value)
			if err != nil {
				return TrashInfo{}, err
			}
			if !strings.HasPrefix(p, "/") {
				return TrashInfo{}, fmt.Errorf("trash: info Path %q is not absolute", p)
			}
			info.Path = p
		case "DeletionDate":
			t, err := time.ParseInLocation(infoDateLayout, value, time.Local)
			if err != nil {
				return TrashInfo{}, fmt.Errorf("trash: info DeletionDate %q: %w", value, err)
			}
			info.DeletionDate = t
		}
	}
	if !seen["Path"] || !seen["DeletionDate"] {
		return TrashInfo{}, errors.New("trash: info file lacks Path or DeletionDate")
	}
	return info, nil
}

// unreservedOrSlash reports whether c is written as itself in Path.
func unreservedOrSlash(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~' || c == '/'
}

func escapePath(p string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if unreservedOrSlash(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func unescapePath(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("trash: info Path %q has a truncated escape", s)
		}
		hi, ok1 := unhex(s[i+1])
		lo, ok2 := unhex(s[i+2])
		if !ok1 || !ok2 {
			return "", fmt.Errorf("trash: info Path %q has an invalid escape", s)
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	out := b.String()
	if strings.IndexByte(out, 0) >= 0 {
		return "", errors.New("trash: info Path holds a NUL byte")
	}
	return out, nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
