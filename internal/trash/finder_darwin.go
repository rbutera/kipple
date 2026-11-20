//go:build darwin && cgo

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

enum { KT_OK = 0, KT_CHANGED = 1, KT_PERMISSION = 2, KT_ERROR = 3 };

static int isPermission(NSError *e) {
	for (NSError *x = e; x != nil; x = x.userInfo[NSUnderlyingErrorKey]) {
		if ([x.domain isEqualToString:NSCocoaErrorDomain] &&
		    (x.code == NSFileWriteNoPermissionError || x.code == NSFileReadNoPermissionError)) {
			return 1;
		}
		if ([x.domain isEqualToString:NSPOSIXErrorDomain] && (x.code == EPERM || x.code == EACCES)) {
			return 1;
		}
	}
	return 0;
}

// kipple_trash_object trashes the object at path, which must be the object
// (dev, ino). It makes a file reference URL (bound to the object, not the
// name), checks that it resolves back to path and that lstat there is
// (dev, ino), and trashes through the reference. Afterwards the reference
// must resolve to the returned path, and, where lstat is allowed there, the
// object there must be (dev, ino).
static int kipple_trash_object(const char *path, unsigned long long dev, unsigned long long ino, char **out) {
	@autoreleasepool {
		NSFileManager *fm = [NSFileManager defaultManager];
		NSString *p = [fm stringWithFileSystemRepresentation:path length:strlen(path)];
		NSURL *ref = [[NSURL fileURLWithPath:p] fileReferenceURL];
		if (ref == nil) {
			*out = strdup("the item has no file reference (it vanished?)");
			return KT_CHANGED;
		}
		NSString *now = [[ref filePathURL] path];
		struct stat st;
		if (now == nil || ![now isEqualToString:p] || lstat([now fileSystemRepresentation], &st) != 0 ||
		    (unsigned long long)st.st_dev != dev || (unsigned long long)st.st_ino != ino) {
			*out = strdup("the item is not the approved object");
			return KT_CHANGED;
		}
		NSURL *result = nil;
		NSError *e = nil;
		if (![fm trashItemAtURL:ref resultingItemURL:&result error:&e] || result == nil) {
			*out = strdup(e ? [[e description] UTF8String] : "NSFileManager returned no destination");
			return e && isPermission(e) ? KT_PERMISSION : KT_ERROR;
		}
		NSString *dest = [result path];
		NSString *where = [[ref filePathURL] path];
		*out = strdup([dest fileSystemRepresentation]);
		if (where == nil || ![where isEqualToString:dest]) {
			return KT_CHANGED;
		}
		if (lstat([dest fileSystemRepresentation], &st) == 0 &&
		    ((unsigned long long)st.st_dev != dev || (unsigned long long)st.st_ino != ino)) {
			return KT_CHANGED;
		}
		return KT_OK;
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io/fs"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/rbutera/kipple/internal/policy"
)

// FinderAvailable reports whether this build has the NSFileManager
// fallback (macOS with cgo). kipple version prints it.
const FinderAvailable = true

// FinderTrasher moves items with NSFileManager trashItemAtURL, which picks
// the Trash for the item's volume (~/.Trash, or <volume>/.Trashes/<uid>),
// resolves name collisions, and returns where the item landed. It works
// without Full Disk Access, which opening ~/.Trash directly needs.
//
// It acts on the object, not the name: the path comes from the verified
// parent descriptor (F_GETPATH) and the stored name, is turned into a file
// reference URL bound to that object, and is trashed through the reference
// only after the reference is confirmed to be the approved (dev, ino).
// Afterwards the parent descriptor must no longer hold the object under
// that name, and the reference must resolve to the returned path.
type FinderTrasher struct {
	// Expect, when set, is the Trash folder the item must arrive in.
	Expect *DirTrasher
}

// NewFinderTrasher returns a FinderTrasher. It fails only in builds without
// cgo (finder_nocgo.go).
func NewFinderTrasher(expect *DirTrasher) (ExpectTrasher, error) {
	return FinderTrasher{Expect: expect}, nil
}

// Trash implements Trasher for whatever object is at name now.
func (f FinderTrasher) Trash(parentFd int, name string) (string, error) {
	var st unix.Stat_t
	if err := fstatat(parentFd, name, &st); err != nil {
		return "", fmt.Errorf("trash: %s: %w", name, err)
	}
	return f.TrashExpect(parentFd, name, uint64(st.Dev), uint64(st.Ino))
}

// TrashExpect implements ExpectTrasher.
func (f FinderTrasher) TrashExpect(parentFd int, name string, dev, ino uint64) (string, error) {
	if !validName(name) {
		return "", ErrInvalidName
	}
	var st unix.Stat_t
	if err := fstatat(parentFd, name, &st); err != nil {
		return "", fmt.Errorf("trash: %s: %w", name, err)
	}
	if uint64(st.Dev) != dev || uint64(st.Ino) != ino {
		return "", ErrItemChanged
	}
	dir, err := policy.DescriptorPath(parentFd)
	if err != nil {
		return "", fmt.Errorf("trash: parent of %s: %w", name, err)
	}
	if f.Expect != nil {
		if err := f.Expect.Verify(); err != nil {
			return "", err
		}
		if dev != f.Expect.dev {
			return "", ErrDifferentDevice
		}
	}
	cs := C.CString(dir + "/" + name)
	defer C.free(unsafe.Pointer(cs))
	// Immediately before the call: the object under that name in the
	// verified parent is still the approved one (by descriptor, not path).
	if err := fstatat(parentFd, name, &st); err != nil {
		return "", fmt.Errorf("trash: %s: %w", name, err)
	}
	if uint64(st.Dev) != dev || uint64(st.Ino) != ino {
		return "", ErrItemChanged
	}
	if beforeFinderCall != nil {
		beforeFinderCall()
	}
	var out *C.char
	code := C.kipple_trash_object(cs, C.ulonglong(dev), C.ulonglong(ino), &out)
	msg := C.GoString(out)
	C.free(unsafe.Pointer(out))
	switch code {
	case C.KT_OK:
	case C.KT_CHANGED:
		if msg != "" && msg[0] == '/' {
			return msg, displaced(msg, "NSFileManager moved something other than the approved item")
		}
		return "", fmt.Errorf("%w: %s", ErrItemChanged, msg)
	case C.KT_PERMISSION:
		return "", fmt.Errorf("trash: NSFileManager: %s: %w", msg, fs.ErrPermission)
	default:
		return "", errors.New("trash: NSFileManager: " + msg)
	}
	dest := msg
	// The approved object must be gone from its parent.
	if err := fstatat(parentFd, name, &st); err == nil && uint64(st.Dev) == dev && uint64(st.Ino) == ino {
		return dest, displaced(dest, "the approved item is still in its folder")
	}
	if f.Expect != nil {
		var pd unix.Stat_t
		if err := unix.Lstat(dest[:len(dest)-len(lastName(dest))-1], &pd); err != nil || uint64(pd.Dev) != f.Expect.dev || uint64(pd.Ino) != f.Expect.ino {
			return dest, errors.New("trash: NSFileManager moved the item outside the expected Trash folder: " + dest)
		}
	}
	return dest, nil
}

func lastName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// beforeFinderCall, in tests, runs between the descriptor pre-check and
// the NSFileManager call.
var beforeFinderCall func()

// displaced describes what NSFileManager moved to dest instead of the
// approved object, with its identity when lstat is allowed there (under
// TCC it may not be).
func displaced(dest, why string) *DisplacedError {
	de := &DisplacedError{Dest: dest, Why: why}
	var st unix.Stat_t
	if unix.Lstat(dest, &st) == nil {
		de.Dev, de.Ino, de.Size, de.Mtime, de.Known = uint64(st.Dev), uint64(st.Ino), st.Size, time.Unix(st.Mtim.Unix()).UTC(), true
	}
	return de
}
