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
//   - the mount holding the real home is the longest mount point that is a
//     prefix of real and has device realDev; the real home's path within
//     its file system is that mount's root plus the rest of real;
//   - every other mount of the same device whose root is that path or an
//     ancestor of it shows the real home at mountpoint + remainder (Real);
//   - every mount of the same device whose root lies inside it has its
//     mount point inside the real home (Inside).
//
// btrfs reports the subvolume in the root field (/@home/me), so roots are
// compared per subvolume. A table without a mount for real, or whose
// device does not match, is an error: the caller refuses.
func AliasesFrom(ms []mountinfo.Mount, real string, realDev uint64) (Aliases, error) {
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
	if holder.Dev() != realDev {
		return Aliases{}, errors.New("the mount table does not match the real home's device")
	}
	fsPath := path.Join(holder.Root, strings.TrimPrefix(real, holder.MountPoint))
	var al Aliases
	for _, m := range ms {
		if m.ID == holder.ID || m.Dev() != realDev {
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
