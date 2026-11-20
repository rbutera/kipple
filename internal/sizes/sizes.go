// Package sizes measures items honestly (design D7): it walks with lstat
// semantics and never follows a symlink, sums st_size for logical bytes and
// st_blocks*512 for allocated bytes, counts a hard-linked inode once, never
// crosses onto another device, and records unreadable parts as a partial
// result instead of failing.
//
// Bytes of entries with more than one hard link are excluded from
// ReclaimableBytes, since moving one link frees nothing while another
// remains. Such entries are also returned in Shared so a scan can count each
// inode once across all its items.
package sizes

import (
	"fmt"
	"io/fs"
	"time"
)

// Status values for Result.Status.
const (
	StatusComplete = "complete"
	StatusPartial  = "partial"
)

// DefaultMaxProtected is how many protected paths a Result lists.
const DefaultMaxProtected = 5

// maxWarnings caps the warnings one item carries.
const maxWarnings = 10

// maxDepth bounds recursion; deeper trees are recorded as partial.
const maxDepth = 256

// Inode is a file identity.
type Inode struct{ Dev, Ino uint64 }

// Usage is the size of one inode.
type Usage struct{ Logical, Allocated int64 }

// Options configure a walk.
type Options struct {
	// Protected, when set, is called for every entry below the root with its
	// root-relative slash path, its name, identity and whether it is a
	// directory. Returning true records
	// the entry in Result.Protected. Entries below a protected directory are
	// still measured but not reported again.
	Protected func(rel, name string, dev, ino uint64, isDir bool) bool
	// ProtectedMode is Protected with the entry's file type (fs.ModeDir,
	// fs.ModeSymlink, 0 for a regular file, and so on); when set it is
	// used instead of Protected.
	ProtectedMode func(rel, name string, dev, ino uint64, mode fs.FileMode) bool
	// MaxProtected caps Result.Protected (DefaultMaxProtected when zero).
	MaxProtected int
	// SkipDir, when set, is called for every directory below the root;
	// returning true leaves it out of the walk entirely (not measured).
	SkipDir func(rel, name string) bool
}

// Result is the measurement of one item.
type Result struct {
	LogicalBytes     int64
	AllocatedBytes   int64
	ReclaimableBytes int64
	FileCount        int64
	// NewestMtime is the newest modification time of any entry, including
	// the root and directories; NewestFileMtime considers non-directories
	// only (zero when there are none).
	NewestMtime     time.Time
	NewestFileMtime time.Time
	Status          string
	Warnings        []string
	// Protected lists up to Options.MaxProtected root-relative paths of
	// protected entries; ProtectedCount counts all of them.
	Protected      []string
	ProtectedCount int
	// Mounts lists root-relative paths where another volume is mounted
	// inside the root; the walk did not cross into them, and they also make
	// the result partial.
	Mounts []string
	// Shared holds every non-directory inode with more than one link that
	// was counted, so callers can dedupe across items.
	Shared map[Inode]Usage
}

func (r *Result) warn(format string, args ...any) {
	r.Status = StatusPartial
	switch {
	case len(r.Warnings) < maxWarnings:
		r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
	case len(r.Warnings) == maxWarnings:
		r.Warnings = append(r.Warnings, "further warnings omitted")
	}
}

func (r *Result) newerFile(t time.Time) {
	r.newer(t)
	if t.After(r.NewestFileMtime) {
		r.NewestFileMtime = t
	}
}

func (r *Result) newer(t time.Time) {
	if t.After(r.NewestMtime) {
		r.NewestMtime = t
	}
}
