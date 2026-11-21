//go:build !unix && kipple_permanent

package apply

import (
	"time"

	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/trash"
)

func removeItem(*policy.Fresh, uint64, uint64, protectFunc, mountFunc, refreshFunc, time.Time) (removal, error) {
	return removal{}, trash.ErrUnsupported
}
