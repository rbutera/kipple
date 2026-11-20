//go:build darwin && cgo

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// kipple_trash asks NSFileManager to move path to the Trash. On success it
// returns the resulting path (malloc'd); on failure NULL with *err set to a
// malloc'd description.
static char *kipple_trash(const char *path, char **err) {
	@autoreleasepool {
		NSString *p = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:path length:strlen(path)];
		NSURL *url = [NSURL fileURLWithPath:p isDirectory:NO];
		NSURL *result = nil;
		NSError *e = nil;
		if (![[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&result error:&e]) {
			*err = strdup(e ? [[e localizedDescription] UTF8String] : "unknown error");
			return NULL;
		}
		if (result == nil) {
			*err = strdup("NSFileManager returned no destination");
			return NULL;
		}
		return strdup([result fileSystemRepresentation]);
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/policy"
)

// FinderTrasher moves items with NSFileManager trashItemAtURL, which picks
// the Trash for the item's volume (~/.Trash, or <volume>/.Trashes/<uid>),
// resolves name collisions, and returns where the item landed.
//
// The API takes a path, so FinderTrasher derives it from the parent
// descriptor with F_GETPATH, checks that the path still reaches the very
// object the descriptor holds, and after the move checks that the object at
// the returned path is that object. When Expect is set, the item must land
// directly inside that verified directory. The descriptor-based DirTrasher
// has no window between check and move; apply uses it by default.
type FinderTrasher struct {
	// Expect, when set, is the Trash folder the item must arrive in.
	Expect *DirTrasher
}

// NewFinderTrasher returns a FinderTrasher. It fails only in builds without
// cgo (finder_nocgo.go).
func NewFinderTrasher(expect *DirTrasher) (Trasher, error) {
	return FinderTrasher{Expect: expect}, nil
}

// Trash implements Trasher.
func (f FinderTrasher) Trash(parentFd int, name string) (string, error) {
	if !validName(name) {
		return "", ErrInvalidName
	}
	var ist unix.Stat_t
	if err := fstatat(parentFd, name, &ist); err != nil {
		return "", fmt.Errorf("trash: %s: %w", name, err)
	}
	dir, err := policy.DescriptorPath(parentFd)
	if err != nil {
		return "", fmt.Errorf("trash: parent of %s: %w", name, err)
	}
	src := filepath.Join(dir, name)
	var pst unix.Stat_t
	if err := unix.Lstat(src, &pst); err != nil || pst.Dev != ist.Dev || pst.Ino != ist.Ino {
		return "", ErrItemChanged
	}
	if f.Expect != nil {
		if err := f.Expect.Verify(); err != nil {
			return "", err
		}
		if uint64(ist.Dev) != f.Expect.dev {
			return "", ErrDifferentDevice
		}
	}
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))
	var cerr *C.char
	res := C.kipple_trash(cs, &cerr)
	if res == nil {
		msg := C.GoString(cerr)
		C.free(unsafe.Pointer(cerr))
		return "", fmt.Errorf("trash: NSFileManager: %s", msg)
	}
	dest := C.GoString(res)
	C.free(unsafe.Pointer(res))

	var dst unix.Stat_t
	if err := unix.Lstat(dest, &dst); err != nil || dst.Dev != ist.Dev || dst.Ino != ist.Ino {
		return dest, fmt.Errorf("%w: %s is not the object that was trashed", ErrItemChanged, dest)
	}
	if f.Expect != nil {
		var pd unix.Stat_t
		if err := unix.Lstat(filepath.Dir(dest), &pd); err != nil || uint64(pd.Dev) != f.Expect.dev || uint64(pd.Ino) != f.Expect.ino {
			return dest, errors.New("trash: NSFileManager moved the item outside the expected Trash folder: " + dest)
		}
	}
	return dest, nil
}
