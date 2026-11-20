//go:build !darwin

package procs

import "context"

type unsupported struct{}

// New returns an Inspector whose every answer is unknown (ErrUnsupported).
// A Linux port reads /proc instead (design D17).
func New() Inspector { return unsupported{} }

func (unsupported) RunningBundleIDs(context.Context) (map[string]bool, error) {
	return nil, ErrUnsupported
}

func (unsupported) RunningNames(context.Context, []string) (map[string]bool, error) {
	return nil, ErrUnsupported
}

func (unsupported) ProcessCwds(context.Context, []string) ([]string, error) {
	return nil, ErrUnsupported
}
