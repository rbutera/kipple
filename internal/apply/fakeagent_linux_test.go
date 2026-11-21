//go:build linux

package apply

import (
	"os"

	"golang.org/x/sys/unix"
)

// fakeAgentNonDumpableEnv asks the fake agent to make itself not dumpable.
const fakeAgentNonDumpableEnv = "KIPPLE_TEST_FAKE_AGENT_NONDUMPABLE"

// fakeAgentSetup runs in the fake agent before it reports ready: asked to,
// it makes itself not dumpable, as some real programs do, so that the
// kernel refuses to show its working directory to other processes.
func fakeAgentSetup() {
	if os.Getenv(fakeAgentNonDumpableEnv) != "" {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			os.Exit(3)
		}
	}
}
