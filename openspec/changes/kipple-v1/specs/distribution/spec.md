# Spec Delta

## Purpose

How kipple is built, checked and released so that a download can be verified and a release is never cut from a red build.

## ADDED Requirements

### Requirement: CI gates
Every push and pull request SHALL run lint (`golangci-lint`), `go vet`, the full test suite with the race detector, the no-real-home check, and a build of all targets on a macOS runner.

#### Scenario: Red CI blocks release
- **WHEN** a `v*` tag is pushed on a commit whose CI run is not green
- **THEN** the release workflow fails before publishing anything

### Requirement: Signed releases
Pushing a `v*` tag SHALL produce a GitHub Release with darwin arm64, amd64 and universal archives, a `checksums.txt`, and a keyless cosign signature and certificate for the checksums. The README SHALL document how to verify them.

#### Scenario: Verify checksums
- **WHEN** a user runs the documented `cosign verify-blob` command on `checksums.txt`
- **THEN** verification succeeds against the repository's workflow identity

### Requirement: Version embedding
Release binaries SHALL report the tag version and commit in `kipple version`.

#### Scenario: Tagged build
- **WHEN** the binary from release v0.1.0 runs `kipple version`
- **THEN** it prints `0.1.0` and the commit hash

### Requirement: Licence and docs
The repository SHALL carry an MIT `LICENSE`, a README with install (binary download, `go install`, Homebrew tap later), safety model, usage, and the agent section, plus `SECURITY.md` describing the safety boundary and how to report a path that escapes it.

#### Scenario: Files present
- **WHEN** the repo is checked out
- **THEN** `LICENSE`, `README.md`, `SECURITY.md` and `llms.txt` exist
