//go:build linux

package trash

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// fsNames maps statfs f_type magic numbers to the names users know.
var fsNames = map[uint32]string{
	unix.EXT4_SUPER_MAGIC:      "ext2/3/4",
	unix.BTRFS_SUPER_MAGIC:     "btrfs",
	unix.XFS_SUPER_MAGIC:       "xfs",
	unix.TMPFS_MAGIC:           "tmpfs",
	unix.RAMFS_MAGIC:           "ramfs",
	unix.OVERLAYFS_SUPER_MAGIC: "overlayfs",
	unix.NFS_SUPER_MAGIC:       "NFS",
	unix.FUSE_SUPER_MAGIC:      "FUSE",
	unix.CIFS_SUPER_MAGIC:      "CIFS/SMB",
	unix.SMB2_SUPER_MAGIC:      "SMB2",
	unix.SMB_SUPER_MAGIC:       "SMB",
	unix.F2FS_SUPER_MAGIC:      "f2fs",
	unix.ECRYPTFS_SUPER_MAGIC:  "eCryptfs",
	unix.V9FS_MAGIC:            "9p",
	unix.MSDOS_SUPER_MAGIC:     "FAT",
	unix.EXFAT_SUPER_MAGIC:     "exFAT",
	unix.BCACHEFS_SUPER_MAGIC:  "bcachefs",
	unix.REISERFS_SUPER_MAGIC:  "reiserfs",
	unix.NILFS_SUPER_MAGIC:     "nilfs2",
	unix.AFS_SUPER_MAGIC:       "AFS",
	unix.CODA_SUPER_MAGIC:      "Coda",
	unix.SQUASHFS_MAGIC:        "squashfs",
	0x2fc12fc1:                 "ZFS",
	0x5346544e:                 "NTFS",
	0x7366746e:                 "NTFS (ntfs3)",
}

// FileSystemName names the file system holding the object open at fd, for
// messages such as "the file system (NFS) cannot rename without
// replacing". An unknown type is reported by its magic number.
func FileSystemName(fd int) string {
	var st unix.Statfs_t
	for {
		err := unix.Fstatfs(fd, &st)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "unknown"
		}
		break
	}
	t := uint32(st.Type) //nolint:gosec // G115: f_type is a 32-bit magic number on every architecture
	if n, ok := fsNames[t]; ok {
		return n
	}
	return fmt.Sprintf("type 0x%x", t)
}
