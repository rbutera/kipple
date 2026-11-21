# Spec Delta

## MODIFIED Requirements

### Requirement: CI gates
Every push and pull request SHALL run lint (`golangci-lint`), `go vet`, the full test suite with the race detector, the no-real-home check, and a build of all targets, on a macOS runner and on an Ubuntu runner. The Ubuntu job SHALL also lint with `GOOS=linux`, build and run the README agent flow with a cgo-free Linux binary against a fixture, compile the GUI with the `gui` tag, run the privileged mount check in a container, and fail if anything under the runner's home folder changed during the tests.

#### Scenario: Red CI blocks release
- **WHEN** a `v*` tag is pushed on a commit whose CI run is not green
- **THEN** the release workflow fails before publishing anything

#### Scenario: Linux failure blocks release
- **WHEN** the Ubuntu job fails and the macOS jobs pass for the tagged commit
- **THEN** the release workflow fails before publishing anything

#### Scenario: Runner home unchanged
- **WHEN** a change makes a test write a file under the Ubuntu runner's home folder
- **THEN** the Ubuntu job fails naming the changed path

### Requirement: Signed releases
Pushing a `v*` tag SHALL produce a GitHub Release with darwin arm64, amd64 and universal archives, linux amd64 and arm64 archives (built without cgo), a `checksums.txt` covering every archive, and a keyless cosign signature and certificate for the checksums. The README SHALL document how to verify them.

#### Scenario: Verify checksums
- **WHEN** a user runs the documented `cosign verify-blob` command on `checksums.txt`
- **THEN** verification succeeds against the repository's workflow identity

#### Scenario: Linux archives in the signed checksums
- **WHEN** `goreleaser release --snapshot --clean --skip=sign` runs
- **THEN** `dist/` holds `kipple_<version>_linux_amd64.tar.gz` and `kipple_<version>_linux_arm64.tar.gz`, both listed in `checksums.txt`, and each binary is statically linked

### Requirement: Licence and docs
The repository SHALL carry an MIT `LICENSE`, a README with install (binary download for macOS and Linux, `go install`, Homebrew tap later), safety model, usage, a Linux section (Trash location, restoring from the file manager, what differs from macOS), the agent section and a roadmap that lists Linux packaging (AUR, deb, rpm, Nix) as not yet available, plus `SECURITY.md` describing the safety boundary and how to report a path that escapes it. docs/safety.md SHALL describe the Linux Trash, restore, denylist additions and known limits. Dry run is the default and the Trash the only destination, and the docs SHALL say so for both platforms.

#### Scenario: Files present
- **WHEN** the repo is checked out
- **THEN** `LICENSE`, `README.md`, `SECURITY.md` and `llms.txt` exist

#### Scenario: Linux install documented
- **WHEN** a reader follows the README's Linux install steps for a release tarball
- **THEN** the steps include checksum and signature verification and end with `kipple version` printing `linux`

## ADDED Requirements

### Requirement: Container test harness
The repository SHALL provide `make test-linux`, which runs `go vet`, the full race-enabled test suite, the no-real-home check and the README agent flow inside a Linux container as a non-root user (the test suite itself SHALL refuse to run as root). The repository SHALL be mounted read-only, no host home folder is mounted, and the container's home is container-local. The run SHALL fail if the container user's home changed. It SHALL also provide `make test-linux-mounts`, which in a throwaway privileged container creates real bind, tmpfs and overlay mounts inside a fixture home only, and checks as a non-root user that kipple marks or refuses them and moves nothing across them. Neither target may create, change or remove anything under any real home folder. Dry run is the default and the Trash the only destination in every check.

#### Scenario: Harness refuses root
- **WHEN** the harness's test script starts as uid 0
- **THEN** it exits non-zero before running any test

#### Scenario: Test suite refuses root
- **WHEN** `go test ./...` runs with effective UID 0 on any platform
- **THEN** every package fails in its test main before running a test, naming root as the reason

#### Scenario: Container home unchanged
- **WHEN** `make test-linux` finishes
- **THEN** it reports that the container user's home is unchanged, or fails naming what changed

#### Scenario: Real mounts checked
- **WHEN** `make test-linux-mounts` runs
- **THEN** a fixture item containing a bind mount is skipped with `contains_mount`, a bind-mounted item and an overlay lower-layer item are not moved, and the fixture's checksum outside the moved items is unchanged
