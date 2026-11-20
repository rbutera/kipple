//go:build unix

package receipt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/trash"
)

// ensureDir creates the receipts folder (0700) if needed.
func (s Store) ensureDir() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	return nil
}

// openDir opens the receipts folder without following a symlink at its
// final component.
func (s Store) openDir() (int, error) {
	for {
		fd, err := unix.Open(s.dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			// With O_DIRECTORY, macOS reports a symlink as ENOTDIR.
			var st unix.Stat_t
			if errors.Is(err, unix.ELOOP) || (errors.Is(err, unix.ENOTDIR) && unix.Lstat(s.dir, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK) {
				return -1, fmt.Errorf("receipt: %s is a symbolic link; refusing to use it", s.dir)
			}
			return fd, err
		}
	}
}

// write stores r as <id>.json through a temporary file in the same folder:
// written, synced, then renamed into place (replacing only when replace is
// set; otherwise an existing receipt yields fs.ErrExist).
func (s Store) write(r *plan.Receipt, replace bool) error {
	data, err := encode(r)
	if err != nil {
		return err
	}
	dfd, err := s.openDir()
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	defer func() { _ = unix.Close(dfd) }()
	tmp := "." + r.ID + "." + NewID(r.CreatedAt)[17:] + ".tmp"
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("receipt: %w", err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(s.dir, tmp))
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		final := r.ID + ".json"
		if replace {
			err = unix.Renameat(dfd, tmp, dfd, final)
		} else {
			err = trash.MoveNoReplace(dfd, tmp, dfd, final)
			if errors.Is(err, unix.EEXIST) {
				err = os.ErrExist
			}
		}
	}
	if err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		if errors.Is(err, os.ErrExist) {
			return err
		}
		return fmt.Errorf("receipt: write %s: %w", r.ID, err)
	}
	return nil
}

// names lists the receipts folder.
func (s Store) names() ([]string, error) {
	fd, err := s.openDir()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), s.dir)
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}

// read decodes the receipt file name in the receipts folder. The folder
// is opened without following a symlink (openDir) and the file relative to
// it, also without following one, so a receipt is only ever read from
// kipple's own receipts folder. It must be a regular file of at most
// plan.MaxDocumentSize bytes; a FIFO does not block the open.
func (s Store) read(name string) (plan.Receipt, error) {
	dfd, err := s.openDir()
	if err != nil {
		return plan.Receipt{}, err
	}
	defer func() { _ = unix.Close(dfd) }()
	path := filepath.Join(s.dir, name)
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ELOOP) {
		return plan.Receipt{}, fmt.Errorf("%s is a symbolic link; refusing to read it", path)
	}
	if err != nil {
		return plan.Receipt{}, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return plan.Receipt{}, err
	}
	if !fi.Mode().IsRegular() {
		return plan.Receipt{}, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, plan.MaxDocumentSize+1))
	if err != nil {
		return plan.Receipt{}, err
	}
	if len(data) > plan.MaxDocumentSize {
		return plan.Receipt{}, fmt.Errorf("%s is larger than %d bytes", path, plan.MaxDocumentSize)
	}
	return plan.DecodeReceipt(bytes.NewReader(data))
}

// Lock takes an exclusive lock (flock) on the receipt id, held until the
// returned function is called, so concurrent restores and saves of one
// receipt never overwrite each other's changes. The lock file .<id>.lock
// lives in the receipts folder, opened without following a symlink.
func (s Store) Lock(id string) (func(), error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("receipt: invalid ID %q", id)
	}
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	dfd, err := s.openDir()
	if err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	defer func() { _ = unix.Close(dfd) }()
	fd, err := unix.Openat(dfd, "."+id+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("receipt: lock %s: %w", id, err)
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX)
		if err != unix.EINTR {
			break
		}
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("receipt: lock %s: %w", id, err)
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = unix.Close(fd)
	}, nil
}
