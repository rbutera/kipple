//go:build !unix

package trash

import "os"

// DirTrasher is unsupported on non-unix platforms.
type DirTrasher struct{}

// OpenDirTrasher is unsupported here.
func OpenDirTrasher(string) (*DirTrasher, error) { return nil, ErrUnsupported }

// NewDirTrasher is unsupported here.
func NewDirTrasher(*os.File, string) (*DirTrasher, error) { return nil, ErrUnsupported }

// Path is empty here.
func (d *DirTrasher) Path() string { return "" }

// Fd is -1 here.
func (d *DirTrasher) Fd() int { return -1 }

// Identity is unknown here.
func (d *DirTrasher) Identity() (dev, ino uint64) { return 0, 0 }

// Close is a no-op here.
func (d *DirTrasher) Close() error { return nil }

// Verify always fails here.
func (d *DirTrasher) Verify() error { return ErrUnsupported }

// Trash always fails here.
func (d *DirTrasher) Trash(int, string) (string, error) { return "", ErrUnsupported }
