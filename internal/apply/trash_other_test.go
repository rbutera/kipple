//go:build !darwin && !linux

package apply

// kipple supports macOS and Linux only; elsewhere there are no platform
// probe cases.
var platformProbeCases []probeCase

const symlinkedTrashIsTrashAccess = false
