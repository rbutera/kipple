//go:build !unix

package sizes

import (
	"context"
	"errors"
	"io/fs"
	"os"
)

var errUnsupported = errors.New("sizes: unsupported platform")

// Measure is unsupported on non-unix platforms.
func Measure(context.Context, string, Options) (Result, error) { return Result{}, errUnsupported }

// MeasureDir is unsupported on non-unix platforms.
func MeasureDir(context.Context, *os.File, Options) (Result, error) {
	return Result{}, errUnsupported
}

// MeasureFile reports the logical size only and marks the result partial.
func MeasureFile(fi fs.FileInfo) Result {
	r := Result{Status: StatusComplete, LogicalBytes: fi.Size(), FileCount: 1, NewestMtime: fi.ModTime()}
	r.warn("allocated size unknown")
	return r
}
