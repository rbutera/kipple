//go:build !kipple_permanent

package apply

import (
	"time"

	"github.com/rbutera/kipple/internal/policy"
)

// PermanentAvailable reports whether this build can delete permanently.
const PermanentAvailable = false

func removeItem(*policy.Fresh, uint64, uint64, protectFunc, time.Time) (removal, error) {
	return removal{}, ErrPermanentUnavailable
}
