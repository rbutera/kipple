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
			if errors.Is(err, unix.ELOOP) {
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

// read decodes the receipt file name in the receipts folder.
func (s Store) read(name string) (plan.Receipt, error) {
	data, err := readFile(filepath.Join(s.dir, name))
	if err != nil {
		return plan.Receipt{}, err
	}
	return plan.DecodeReceipt(bytes.NewReader(data))
}

// readFile reads a regular, non-symlinked file of at most
// plan.MaxDocumentSize bytes without blocking on a FIFO.
func readFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ELOOP) {
		return nil, fmt.Errorf("%s is a symbolic link; refusing to read it", path)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, plan.MaxDocumentSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > plan.MaxDocumentSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, plan.MaxDocumentSize)
	}
	return data, nil
}
