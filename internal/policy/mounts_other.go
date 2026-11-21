//go:build !linux

package policy

// systemMounts: outside Linux every mount has its own st_dev, which the
// walks already check.
func systemMounts(string, int) MountOracle { return noMounts{} }

// noMounts is the oracle where a mount always changes st_dev (macOS):
// no entry needs another check.
type noMounts struct{}

func (noMounts) Available() error { return nil }
func (noMounts) MountRoot(int, string, uint64, uint64) (bool, error) {
	return false, nil
}
