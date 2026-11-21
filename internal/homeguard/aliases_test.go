package homeguard_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/rbutera/kipple/internal/homeguard"
	"github.com/rbutera/kipple/internal/mountinfo"
)

func mounts(t *testing.T, lines ...string) []mountinfo.Mount {
	t.Helper()
	var out []mountinfo.Mount
	for _, l := range lines {
		m, err := mountinfo.ParseLine(l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

// Design L2: where the real home is also visible, from the mount table.
func TestAliasesFrom(t *testing.T) {
	root := "1 0 8:1 / / rw - ext4 /dev/sda1 rw"
	dev := mountinfo.Mount{Major: 8, Minor: 1}.Dev()
	for _, tc := range []struct {
		name       string
		table      []string
		real       string
		dev        uint64
		wantReal   []string
		wantInside []string
		wantErr    bool
	}{
		{"Bind mount of a folder inside home", []string{root, "50 1 8:1 /home/me/sub /mnt/x rw - ext4 /dev/sda1 rw"}, "/home/me", dev, nil, []string{"/mnt/x"}, false},
		{"Bind mount of the home's parent", []string{root, "51 1 8:1 /home /mnt/h rw - ext4 /dev/sda1 rw"}, "/home/me", dev, []string{"/mnt/h/me"}, nil, false},
		{"Bind mount of the home itself", []string{root, "52 1 8:1 /home/me /srv/me rw - ext4 /dev/sda1 rw"}, "/home/me", dev, []string{"/srv/me"}, nil, false},
		{"Another file system is ignored", []string{root, "53 1 0:40 / /tmp rw - tmpfs tmpfs rw"}, "/home/me", dev, nil, nil, false},
		{"btrfs subvolume roots", []string{
			"1 0 0:30 /@ / rw - btrfs /dev/nvme0n1p2 rw,subvol=/@",
			"2 1 0:30 /@home /home rw - btrfs /dev/nvme0n1p2 rw,subvol=/@home",
			"3 1 0:30 /@home/me/proj /work rw - btrfs /dev/nvme0n1p2 rw,subvol=/@home",
			"4 1 0:30 /@home /mnt/b rw - btrfs /dev/nvme0n1p2 rw,subvol=/@home",
		}, "/home/me", mountinfo.Mount{Major: 0, Minor: 30}.Dev(), []string{"/mnt/b/me"}, []string{"/work"}, false},
		{"No mount holds the home", []string{"53 1 0:40 / /tmp rw - tmpfs tmpfs rw"}, "/home/me", dev, nil, nil, true},
		{"Device mismatch", []string{root}, "/home/me", dev + 1, nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, err := homeguard.AliasesFrom(mounts(t, tc.table...), tc.real, tc.dev)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(al.Real, tc.wantReal) || !slices.Equal(al.Inside, tc.wantInside) {
				t.Fatalf("aliases %+v, want real %q inside %q", al, tc.wantReal, tc.wantInside)
			}
		})
	}
}

// specs/safety-policy "Fixture home never overlaps the real home" on Linux,
// with a stub file system modelling bind mounts.
func TestRelateThroughBindMounts(t *testing.T) {
	root := homeguard.ID{Dev: 1, Ino: 2}
	home := homeguard.ID{Dev: 1, Ino: 10}
	me := homeguard.ID{Dev: 1, Ino: 11}
	sub := homeguard.ID{Dev: 1, Ino: 12}
	mnt := homeguard.ID{Dev: 1, Ino: 20}
	tmp := homeguard.ID{Dev: 2, Ino: 30}
	fx := homeguard.ID{Dev: 2, Ino: 31}
	s := stub{
		chains: map[string][]homeguard.ID{
			"/home/me":     {me, home, root},
			"/home/me/sub": {sub, me, home, root},
			"/mnt/x":       {sub, mnt, root}, // bind of ~/sub: ".." is /mnt
			"/mnt/h/me":    {me, home, mnt, root},
			"/mnt/h":       {home, mnt, root},
			"/tmp/fx":      {fx, tmp, root},
			"/mnt":         {mnt, root},
		},
		stats: map[string]homeguard.ID{"/mnt/x": sub, "/mnt/h/me": me},
	}
	fsys := s.fs()
	fsys.Aliases = func(string) (homeguard.Aliases, error) {
		return homeguard.Aliases{Real: []string{"/mnt/h/me"}, Inside: []string{"/mnt/x"}}, nil
	}
	for _, tc := range []struct {
		name, candidate string
		want            homeguard.Relation
	}{
		{"KIPPLE_HOME through a bind mount of a folder inside home", "/mnt/x", homeguard.Inside},
		{"KIPPLE_HOME through a bind mount of the home's parent", "/mnt/h/me", homeguard.Same},
		{"Parent of the bind mount contains the real home", "/mnt", homeguard.Contains},
		{"Separate temp folder accepted", "/tmp/fx", homeguard.Separate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := homeguard.Relate(fsys, tc.candidate, "/home/me")
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v, want %v", got, err, tc.want)
			}
		})
	}
	t.Run("mountinfo unreadable refuses", func(t *testing.T) {
		bad := fsys
		bad.Aliases = func(string) (homeguard.Aliases, error) { return homeguard.Aliases{}, os.ErrPermission }
		if _, err := homeguard.Relate(bad, "/tmp/fx", "/home/me"); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("err = %v, want a refusal", err)
		}
	})
}
