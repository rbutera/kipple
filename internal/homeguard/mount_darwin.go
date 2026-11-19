//go:build darwin

package homeguard

import "golang.org/x/sys/unix"

// mountPoint returns statfs f_mntonname for path, following symlinks.
func mountPoint(path string) (string, error) {
	var st unix.Statfs_t
	for {
		err := unix.Statfs(path, &st)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "", err
		}
		return unix.ByteSliceToString(st.Mntonname[:]), nil
	}
}
