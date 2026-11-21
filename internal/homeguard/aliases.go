package homeguard

import (
	"errors"
	"path"
	"strings"

	"github.com/rbutera/kipple/internal/mountinfo"
)

// AliasesFrom computes, from a parsed mount table, where the real home
// (an absolute, symlink-resolved path whose files have st_dev realDev) is
// also visible (design L2):
//
//   - the mount holding the real home is the one with the longest mount
//     point that is a prefix of real. It is confirmed when its mountinfo
//     device is realDev, or when mountDev (an fstat of an O_PATH open of
//     the mount point) reports realDev, or when it is btrfs, whose stat
//     device is per subvolume and never mountinfo's (the superblock's);
//   - the real home's path within its file system is that mount's root
//     plus the rest of real (btrfs puts the subvolume in the root, such as
//     /@home/me, so the comparison works per subvolume);
//   - every other mount of the holder's file system (mountinfo
//     major:minor) whose root is that path or an ancestor of it shows the
//     real home at mountpoint + remainder (Real);
//   - every mount of it whose root lies inside that path has its mount
//     point inside the real home (Inside).
//
// A table without a mount for real, or one that cannot be confirmed, is an
// error: the caller refuses.
func AliasesFrom(ms []mountinfo.Mount, real string, realDev uint64, mountDev func(mountPoint string) (uint64, error)) (Aliases, error) {
	var holder *mountinfo.Mount
	for i := range ms {
		m := &ms[i]
		if !under(real, m.MountPoint) {
			continue
		}
		if holder == nil || len(m.MountPoint) >= len(holder.MountPoint) {
			holder = m
		}
	}
	if holder == nil {
		return Aliases{}, errors.New("no mount holds the real home")
	}
	confirmed := holder.Dev() == realDev || holder.FSType == "btrfs"
	if !confirmed && mountDev != nil {
		if d, err := mountDev(holder.MountPoint); err == nil && d == realDev {
			confirmed = true
		}
	}
	if !confirmed {
		return Aliases{}, errors.New("the mount table does not match the real home's device")
	}
	fsPath := path.Join(holder.Root, strings.TrimPrefix(real, holder.MountPoint))
	var al Aliases
	for _, m := range ms {
		if m.ID == holder.ID || m.Major != holder.Major || m.Minor != holder.Minor {
			continue
		}
		switch {
		case under(fsPath, m.Root):
			al.Real = append(al.Real, path.Join(m.MountPoint, strings.TrimPrefix(fsPath, m.Root)))
		case under(m.Root, fsPath):
			al.Inside = append(al.Inside, m.MountPoint)
		}
	}
	return al, nil
}

// under reports whether p is dir or lies below it (clean absolute paths).
func under(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}
