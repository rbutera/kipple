//go:build !unix

package config

import (
	"fmt"
	"io/fs"
	"os"
)

// openConfig refuses a symlink by lstat on platforms without O_NOFOLLOW.
// kipple targets macOS; this keeps the package building elsewhere.
func openConfig(path string) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symbolic link; kipple only reads a regular config file", path)
	}
	return os.Open(path)
}
