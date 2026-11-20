//go:build !unix

package apply

import (
	"os"

	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

func openItem(*policy.Fresh, uint64, uint64) (*os.File, error) { return nil, trash.ErrUnsupported }
func withOwnerWrite(*os.File, func() error) error              { return trash.ErrUnsupported }
func isPermission(error) bool                                  { return false }

func classify(error) string { return plan.SkipMoveFailed }
func openHomeTrash(*policy.Session, string) (*trash.DirTrasher, error) {
	return nil, trash.ErrUnsupported
}

func tagTarget(*policy.Fresh, uint64, uint64, *os.File) (int, func(), error) {
	return -1, nil, trash.ErrUnsupported
}

func openEntry(int, string) (int, entryInfo, error) { return -1, entryInfo{}, trash.ErrUnsupported }

func moveBack(int, string, *policy.Fresh, entryInfo, []byte) error { return trash.ErrUnsupported }

const restoreSwapNote = ""

type errRestoreArrived struct{ note string }

func (e *errRestoreArrived) Error() string { return e.note }

func closeFd(int) {}

func dupFile(int, string) (*os.File, error) { return nil, trash.ErrUnsupported }

func findInTrash(int, uint64, uint64) (string, bool) { return "", false }

func stillAt(string, uint64, uint64) bool { return false }

func tagDisplaced(string, uint64, uint64, []byte) error { return trash.ErrUnsupported }
