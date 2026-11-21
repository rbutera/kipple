//go:build gui && !darwin

package main

import (
	"os"
	"testing"

	"github.com/rbutera/kipple/internal/testutil"
)

// TestMain off macOS: only the platform refusal test builds here (the GUI
// tests are gui && darwin), and it runs under the same root guard.
func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }
