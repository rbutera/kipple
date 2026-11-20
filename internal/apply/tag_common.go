package apply

import (
	"fmt"

	"github.com/rbutera/kipple/internal/policy"
)

// tagValue is the tag for item itemID moved under receipt receiptID from
// the home whose identity is home.
func tagValue(receiptID, itemID string, home *policy.FileIdentity) []byte {
	var dev, ino uint64
	if home != nil {
		dev, ino = home.Dev, home.Ino
	}
	return []byte(fmt.Sprintf("kipple/1 receipt=%s item=%s home=%d:%d", receiptID, itemID, dev, ino))
}
