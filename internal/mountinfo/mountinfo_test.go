package mountinfo

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func TestParseLine(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       Mount
	}{
		{"proc(5) example", `36 35 98:0 /mnt1 /mnt/parent rw,noatime master:1 - ext3 /dev/root rw,errors=continue`,
			Mount{ID: 36, ParentID: 35, Major: 98, Minor: 0, Root: "/mnt1", MountPoint: "/mnt/parent", Options: "rw,noatime", Optional: []string{"master:1"}, FSType: "ext3", Source: "/dev/root", SuperOptions: "rw,errors=continue"}},
		{"no optional fields", `412 410 0:71 / /proc rw,nosuid - proc proc rw`,
			Mount{ID: 412, ParentID: 410, Major: 0, Minor: 71, Root: "/", MountPoint: "/proc", Options: "rw,nosuid", Optional: []string{}, FSType: "proc", Source: "proc", SuperOptions: "rw"}},
		{"several optional fields", `40 1 8:1 /home/me /mnt/x rw shared:2 master:3 - ext4 /dev/sda1 rw`,
			Mount{ID: 40, ParentID: 1, Major: 8, Minor: 1, Root: "/home/me", MountPoint: "/mnt/x", Options: "rw", Optional: []string{"shared:2", "master:3"}, FSType: "ext4", Source: "/dev/sda1", SuperOptions: "rw"}},
		{"octal escapes", `41 1 0:30 /@home/my\040docs /mnt/a\134b\011c rw - btrfs /dev/nvme0n1p2 rw,subvol=/@home`,
			Mount{ID: 41, ParentID: 1, Major: 0, Minor: 30, Root: "/@home/my docs", MountPoint: "/mnt/a\\b\tc", Options: "rw", Optional: []string{}, FSType: "btrfs", Source: "/dev/nvme0n1p2", SuperOptions: "rw,subvol=/@home"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseLine(tc.line)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.want.ID || got.ParentID != tc.want.ParentID || got.Major != tc.want.Major || got.Minor != tc.want.Minor ||
				got.Root != tc.want.Root || got.MountPoint != tc.want.MountPoint || got.Options != tc.want.Options ||
				strings.Join(got.Optional, ",") != strings.Join(tc.want.Optional, ",") || got.FSType != tc.want.FSType ||
				got.Source != tc.want.Source || got.SuperOptions != tc.want.SuperOptions {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestParseLineMalformed(t *testing.T) {
	for _, line := range []string{
		``,
		`36 35 98:0 /mnt1 /mnt/parent rw,noatime master:1 ext3 /dev/root rw`, // no separator
		`36 35 98:0 /mnt1 /mnt/parent rw - ext3 /dev/root`,                   // missing super options
		`x 35 98:0 / / rw - ext3 /dev/root rw`,
		`36 35 980 / / rw - ext3 /dev/root rw`,
		`36 35 98:x / / rw - ext3 /dev/root rw`,
		`36 35 98:0 relative / rw - ext3 /dev/root rw`,
		`36 35 98:0 / /a\04 rw - ext3 /dev/root rw`,  // truncated escape
		`36 35 98:0 / /a\089 rw - ext3 /dev/root rw`, // not octal
		`36 35 98:0 / /a\777 rw - ext3 /dev/root rw`, // not a byte
		`36 35 98:0 / / rw - ext3 /dev/root rw extra`,
	} {
		if _, err := ParseLine(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseLine(%q) = %v, want ErrMalformed", line, err)
		}
	}
}

func TestParse(t *testing.T) {
	ms, err := Parse(strings.NewReader("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n2 1 0:5 / /tmp rw - tmpfs tmpfs rw\n"))
	if err != nil || len(ms) != 2 || ms[1].MountPoint != "/tmp" {
		t.Fatalf("%+v %v", ms, err)
	}
	if _, err := Parse(strings.NewReader("1 0 8:1 / / rw - ext4 /dev/sda1 rw\nbroken\n")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("one bad line must fail the parse: %v", err)
	}
	if _, err := Parse(strings.NewReader("")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("an empty table must fail: %v", err)
	}
}

func TestDev(t *testing.T) {
	// makedev(8, 1) = 0x801; makedev(259, 65536) as glibc encodes it.
	if got := (Mount{Major: 8, Minor: 1}).Dev(); got != 0x801 {
		t.Fatalf("Dev = %#x", got)
	}
	if got := (Mount{Major: 259, Minor: 65536}).Dev(); got != 0x10010300 {
		t.Fatalf("Dev = %#x", got)
	}
}
