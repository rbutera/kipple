//go:build unix

package sizes

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// deviceHook lets tests simulate a volume mounted inside an item.
var deviceHook func(rel string, dev uint64) uint64

// Measure opens path (never following a symlink in its last component) and
// measures it. Callers that walk from a verified descriptor, such as scan,
// use MeasureDir or MeasureFile instead.
func Measure(ctx context.Context, path string, opts Options) (Result, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return Result{}, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return measureStat(&st), nil
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return Result{}, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	return MeasureDir(ctx, f, opts)
}

// MeasureFile measures a single non-directory item from its lstat info.
func MeasureFile(fi fs.FileInfo) Result {
	if st, ok := fi.Sys().(*unix.Stat_t); ok {
		return measureStat(st)
	}
	r := Result{Status: StatusComplete, LogicalBytes: fi.Size(), FileCount: 1, NewestMtime: fi.ModTime()}
	r.warn("allocated size unknown")
	return r
}

func measureStat(st *unix.Stat_t) Result {
	r := Result{Status: StatusComplete, FileCount: 1}
	alloc := st.Blocks * 512
	r.LogicalBytes = st.Size
	r.AllocatedBytes = alloc
	if st.Nlink > 1 {
		r.Shared = map[Inode]Usage{{uint64(st.Dev), st.Ino}: {st.Size, alloc}}
	} else {
		r.ReclaimableBytes = alloc
	}
	r.newerFile(time.Unix(st.Mtim.Unix()))
	return r
}

// MeasureDir measures the directory open at dir, which must have been opened
// with O_DIRECTORY|O_NOFOLLOW. The walk is relative to that descriptor:
// every entry is inspected with fstatat(AT_SYMLINK_NOFOLLOW) and every
// subdirectory opened with openat(O_NOFOLLOW|O_DIRECTORY) and checked to be
// the object just inspected. It stops at a device change and records it.
// The error is non-nil only when ctx is cancelled or the root cannot be
// inspected.
func MeasureDir(ctx context.Context, dir *os.File, opts Options) (Result, error) {
	if opts.MaxProtected == 0 {
		opts.MaxProtected = DefaultMaxProtected
	}
	var st unix.Stat_t
	if err := fstat(int(dir.Fd()), &st); err != nil {
		return Result{}, err
	}
	w := &walker{ctx: ctx, opts: opts, dev: uint64(st.Dev), r: Result{Status: StatusComplete}}
	w.r.AllocatedBytes += st.Blocks * 512
	w.r.ReclaimableBytes += st.Blocks * 512
	w.r.newer(time.Unix(st.Mtim.Unix()))
	err := w.dir(int(dir.Fd()), "", 0, false)
	if w.r.Status == "" {
		w.r.Status = StatusComplete
	}
	return w.r, err
}

type walker struct {
	ctx  context.Context
	opts Options
	dev  uint64
	r    Result
}

func join(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// dir walks the directory open at fd (not closed here). rel is its
// root-relative path; inProtected is set below a protected directory.
func (w *walker) dir(fd int, rel string, depth int, inProtected bool) error {
	if err := w.ctx.Err(); err != nil {
		w.r.warn("scan cancelled")
		return err
	}
	if depth >= maxDepth {
		w.r.warn("%s: deeper than %d levels, not measured", relOrDot(rel), maxDepth)
		return nil
	}
	lfd, err := openDir(fd, ".")
	if err != nil {
		w.r.warn("%s: cannot be read: %v", relOrDot(rel), err)
		return nil
	}
	lf := os.NewFile(uintptr(lfd), relOrDot(rel))
	defer func() { _ = lf.Close() }()
	for {
		names, rerr := lf.Readdirnames(1024)
		for _, name := range names {
			if err := w.entry(fd, rel, name, depth, inProtected); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			w.r.warn("%s: listing incomplete: %v", relOrDot(rel), rerr)
			return nil
		}
	}
}

func (w *walker) entry(fd int, rel, name string, depth int, inProtected bool) error {
	p := join(rel, name)
	var st unix.Stat_t
	if err := fstatat(fd, name, &st); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil // removed while we walked
		}
		w.r.warn("%s: cannot be inspected: %v", p, err)
		return nil
	}
	dev := uint64(st.Dev)
	if deviceHook != nil {
		dev = deviceHook(p, dev)
	}
	if dev != w.dev {
		w.r.warn("%s: on another volume, not measured", p)
		return nil
	}
	protected := inProtected
	if !inProtected && w.opts.Protected != nil && w.opts.Protected(p, name, dev, st.Ino, st.Mode&unix.S_IFMT == unix.S_IFDIR) {
		protected = true
		w.r.ProtectedCount++
		if len(w.r.Protected) < w.opts.MaxProtected {
			w.r.Protected = append(w.r.Protected, p)
		}
	}
	alloc := st.Blocks * 512
	if st.Mode&unix.S_IFMT == unix.S_IFDIR {
		if w.opts.SkipDir != nil && w.opts.SkipDir(p, name) {
			return nil
		}
		w.r.newer(time.Unix(st.Mtim.Unix()))
		w.r.AllocatedBytes += alloc
		w.r.ReclaimableBytes += alloc
		cfd, err := openDir(fd, name)
		if err != nil {
			w.r.warn("%s: cannot be read: %v", p, err)
			return nil
		}
		defer func() { _ = unix.Close(cfd) }()
		var cst unix.Stat_t
		if err := fstat(cfd, &cst); err != nil || uint64(cst.Dev) != uint64(st.Dev) || cst.Ino != st.Ino {
			w.r.warn("%s: changed while it was measured", p)
			return nil
		}
		return w.dir(cfd, p, depth+1, protected)
	}
	// Files, symlinks (never followed) and other entries.
	w.r.newerFile(time.Unix(st.Mtim.Unix()))
	w.r.FileCount++
	if st.Nlink > 1 {
		id := Inode{dev, st.Ino}
		if w.r.Shared == nil {
			w.r.Shared = map[Inode]Usage{}
		}
		if _, seen := w.r.Shared[id]; seen {
			return nil
		}
		w.r.Shared[id] = Usage{st.Size, alloc}
		w.r.LogicalBytes += st.Size
		w.r.AllocatedBytes += alloc
		return nil
	}
	w.r.LogicalBytes += st.Size
	w.r.AllocatedBytes += alloc
	w.r.ReclaimableBytes += alloc
	return nil
}

func relOrDot(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func openDir(dirfd int, name string) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func fstatat(dirfd int, name string, st *unix.Stat_t) error {
	for {
		err := unix.Fstatat(dirfd, name, st, unix.AT_SYMLINK_NOFOLLOW)
		if err != unix.EINTR {
			return err
		}
	}
}

func fstat(fd int, st *unix.Stat_t) error {
	for {
		err := unix.Fstat(fd, st)
		if err != unix.EINTR {
			return err
		}
	}
}
