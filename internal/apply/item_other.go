//go:build !unix

package apply

import (
	"io/fs"
	"os"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

func openItem(*policy.Fresh, uint64, uint64) (*os.File, error) { return nil, trash.ErrUnsupported }
func withOwnerWrite(*os.File, func() error) error              { return trash.ErrUnsupported }
func isPermission(error) bool                                  { return false }

type protectFunc func(rel, name string, dev, ino uint64, mode fs.FileMode) (string, bool)

type removal struct{ count, bytes int64 }

func removeItem(*policy.Fresh, uint64, uint64, protectFunc) (removal, error) {
	return removal{}, trash.ErrUnsupported
}
func classify(error) string { return plan.SkipMoveFailed }
func openHomeTrash(*policy.Session, string) (*trash.DirTrasher, error) {
	return nil, trash.ErrUnsupported
}

func moveBack(int, string, *policy.Fresh, uint64, uint64, bool) error { return trash.ErrUnsupported }

type fdSource struct{ tr *trash.DirTrasher }

func (s fdSource) dir() string                                    { return "" }
func (s fdSource) stat(string) (entryInfo, error)                 { return entryInfo{}, trash.ErrUnsupported }
func (s fdSource) moveOut(string, *policy.Fresh, entryInfo) error { return trash.ErrUnsupported }

type pathSource struct{ d string }

func (s pathSource) dir() string                                    { return s.d }
func (s pathSource) stat(string) (entryInfo, error)                 { return entryInfo{}, trash.ErrUnsupported }
func (s pathSource) moveOut(string, *policy.Fresh, entryInfo) error { return trash.ErrUnsupported }
