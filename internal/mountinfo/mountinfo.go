// Package mountinfo parses Linux /proc/<pid>/mountinfo (proc(5)). It is
// pure: it reads from an io.Reader and never touches the file system, so
// its tests run on any host. internal/policy uses it as the fallback for
// mount-point detection and internal/homeguard to find where the real home
// is visible through bind mounts (linux-port design L2, L3).
package mountinfo

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Mount is one line of mountinfo.
type Mount struct {
	ID, ParentID int
	// Major and Minor are the st_dev of files on this mount.
	Major, Minor uint32
	// Root is the path, within the file system, of the directory mounted
	// here (for a bind mount, the source directory; for btrfs, it includes
	// the subvolume, such as /@home/me).
	Root string
	// MountPoint is where it is mounted, relative to the process's root.
	MountPoint string
	// Options are the per-mount options.
	Options string
	// Optional are the optional fields (shared:N, master:N, ...).
	Optional []string
	FSType   string
	Source   string
	// SuperOptions are the per-superblock options.
	SuperOptions string
}

// ErrMalformed: a line does not have mountinfo's shape, or an escape is
// not a valid octal byte. Callers fail closed on it.
var ErrMalformed = errors.New("mountinfo: malformed")

// maxLines bounds a parse; no real mount table is this long.
const maxLines = 1 << 20

// Parse reads every line of r. Any malformed line fails the whole parse,
// so a caller never decides from a partial table.
func Parse(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		if n > maxLines {
			return nil, fmt.Errorf("%w: more than %d lines", ErrMalformed, maxLines)
		}
		line := sc.Text()
		if line == "" {
			continue
		}
		m, err := ParseLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no mounts", ErrMalformed)
	}
	return out, nil
}

// ParseLine parses one mountinfo line:
//
//	36 35 98:0 /mnt1 /mnt/parent rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//
// Fields are separated by single spaces; the optional fields end at a lone
// "-". Root, MountPoint and Source have " ", tab, newline and "\" escaped
// as octal (\040, \011, \012, \134), which are decoded.
func ParseLine(line string) (Mount, error) {
	f := strings.Split(line, " ")
	sep := -1
	for i := 6; i < len(f); i++ {
		if f[i] == "-" {
			sep = i
			break
		}
	}
	if len(f) < 10 || sep < 0 || len(f) != sep+4 {
		return Mount{}, fmt.Errorf("%w: %q", ErrMalformed, line)
	}
	var m Mount
	var err error
	if m.ID, err = strconv.Atoi(f[0]); err != nil {
		return Mount{}, fmt.Errorf("%w: mount ID %q", ErrMalformed, f[0])
	}
	if m.ParentID, err = strconv.Atoi(f[1]); err != nil {
		return Mount{}, fmt.Errorf("%w: parent ID %q", ErrMalformed, f[1])
	}
	maj, min, ok := strings.Cut(f[2], ":")
	if !ok {
		return Mount{}, fmt.Errorf("%w: device %q", ErrMalformed, f[2])
	}
	mj, err1 := strconv.ParseUint(maj, 10, 32)
	mn, err2 := strconv.ParseUint(min, 10, 32)
	if err1 != nil || err2 != nil {
		return Mount{}, fmt.Errorf("%w: device %q", ErrMalformed, f[2])
	}
	m.Major, m.Minor = uint32(mj), uint32(mn)
	if m.Root, err = Unescape(f[3]); err != nil {
		return Mount{}, err
	}
	if m.MountPoint, err = Unescape(f[4]); err != nil {
		return Mount{}, err
	}
	if !strings.HasPrefix(m.Root, "/") || !strings.HasPrefix(m.MountPoint, "/") {
		return Mount{}, fmt.Errorf("%w: root %q or mount point %q is not absolute", ErrMalformed, m.Root, m.MountPoint)
	}
	m.Options = f[5]
	m.Optional = append([]string(nil), f[6:sep]...)
	m.FSType = f[sep+1]
	if m.Source, err = Unescape(f[sep+2]); err != nil {
		return Mount{}, err
	}
	m.SuperOptions = f[sep+3]
	return m, nil
}

// Unescape decodes the kernel's octal escapes (\NNN). A backslash not
// followed by three octal digits of a byte value is malformed.
func Unescape(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 >= len(s) {
			return "", fmt.Errorf("%w: truncated escape in %q", ErrMalformed, s)
		}
		v := 0
		for _, c := range []byte(s[i+1 : i+4]) {
			if c < '0' || c > '7' {
				return "", fmt.Errorf("%w: invalid escape in %q", ErrMalformed, s)
			}
			v = v*8 + int(c-'0')
		}
		if v > 255 {
			return "", fmt.Errorf("%w: invalid escape in %q", ErrMalformed, s)
		}
		b.WriteByte(byte(v))
		i += 3
	}
	return b.String(), nil
}

// Dev returns the st_dev value of files on m, as Linux encodes it
// (makedev): what unix.Stat_t.Dev holds.
func (m Mount) Dev() uint64 {
	maj, min := uint64(m.Major), uint64(m.Minor)
	return (maj&0x00000fff)<<8 | (maj&0xfffff000)<<32 | (min & 0x000000ff) | (min&0xffffff00)<<12
}
