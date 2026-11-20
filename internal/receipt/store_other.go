//go:build !unix

package receipt

import "github.com/rbutera/kipple/internal/plan"

var errUnsupported = trashUnsupported{}

type trashUnsupported struct{}

func (trashUnsupported) Error() string { return "receipt: not supported on this platform" }

func (s Store) ensureDir() error                  { return errUnsupported }
func (s Store) write(*plan.Receipt, bool) error   { return errUnsupported }
func (s Store) names() ([]string, error)          { return nil, errUnsupported }
func (s Store) read(string) (plan.Receipt, error) { return plan.Receipt{}, errUnsupported }
func readFile(string) ([]byte, error)             { return nil, errUnsupported }
