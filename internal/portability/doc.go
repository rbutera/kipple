// Package portability holds only a test: it checks the design D17 seams,
// that macOS-only code stays in files built only on macOS and that no file
// built on other platforms refers to it. A later Linux port then adds
// files rather than refactoring.
package portability
