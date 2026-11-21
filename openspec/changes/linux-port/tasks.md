# Tasks

Waves for the /wave skill. Each wave ends with `make check` green on macOS and `make test-linux` green in the Linux container. `make test-linux` runs the whole default suite (`go test -race -count=1 ./...`) as an unprivileged user from task 1.1 on. Every filesystem test runs against a `testutil.NewHome` fixture with `KIPPLE_HOME` and `KIPPLE_STATE_DIR` set; nothing ever touches the real home of the Mac, the colima VM, the container user or a CI runner. Test names follow the spec scenarios (`specs/<capability>/spec.md`). A test that cannot hold on Linux gets a build constraint (`_darwin_test.go`); no test is skipped at run time because of the platform.

Baseline before wave 1 (design Context): in the container, 14 packages pass and 10 fail, with 92 failing tests, mostly missing Trash, move and procs seams. The baseline ran as root, so its permission-test failures do not count.

## 1. Wave 1: harness and Linux mechanics, so the whole default suite passes on Linux

Exit criterion: `make test-linux` is green for every package, run as uid 1000, with darwin-only tests excluded by build constraints only. This wave is not releasable on its own: same-device bind mounts become a boundary in wave 2.

- [x] 1.1 Add the container harness (design L12). It has three parts:
  - `test/linux/Dockerfile`: `golang:1.23-bookworm` pinned by digest, user `kipple` uid 1000 with home `/home/kipple`, `USER kipple`.
  - `test/linux/run.sh`: refuses uid 0; a preflight sets a `user.` xattr in `$TMPDIR`; snapshots `find $HOME -printf '%p %s %T@ %i\n'` before and after; runs `go vet ./...`, `go test -race -count=1 ./...`, `scripts/check-no-real-home.sh` and `scripts/agent-flow-check.sh`.
  - Makefile targets `test-linux` and `test-linux-prime`: repo and host module cache mounted read-only, `GOPROXY=off`, `GOFLAGS=-buildvcs=false -mod=readonly`, a named volume for `GOCACHE`, tmpfs `/tmp`, a second tmpfs at `/xdev` for cross-device tests.

  Verify: "Harness refuses root" (`docker run --user 0 ... test/linux/run.sh` exits non-zero), and a scratch run that touches `$HOME/x` fails naming it ("Container home unchanged").
- [x] 1.2 Make the suite refuse root on every platform: `testutil.GuardedMain` fails the package before any test when the effective UID is 0, and `TestSuiteIsNotRoot` in `internal/testutil` asserts it. Verify: "Test suite refuses root" (run the testutil package under `docker run --user 0` and see it fail naming root), and `make check` is still green on macOS.
- [x] 1.3 Run the baseline again as uid 1000 and record in the commit message which of the 92 failures remain and why. Tag the genuinely darwin-only tests with build constraints:
  - APFS normalisation and alias cases such as `TestNFDHomeIsRespelledToStoredNFC` and the on-disk `Documentſ` cases, into `*_darwin_test.go`;
  - FinderTrasher and Trash-fallback tests (`TestTrashFallback`, `TestProbeTrash` fallback cases);
  - GUI tests (`gui && darwin`).

  Fix portable tests that assumed macOS layout (the `.Trash` path, darwin `Stat_t` field widths, `Library/` fixtures used only to locate the Trash). Verify: `make check` unchanged on macOS, and every remaining Linux failure maps to a later wave-1 task.
- [x] 1.4 Narrow the stub files (`move_other.go`, `tag_other.go`, `procs_other.go`, `mount_unix_other.go`, `finder_nocgo.go` as needed) to `!darwin && !linux`, add Linux files, and extend `internal/portability` so `linux` is checked like `darwin`. Verify: `GOOS=linux go vet ./...`, `GOOS=freebsd go vet ./...` and `go test ./internal/portability` pass, and a deliberately misplaced Linux-only symbol in a temp copy fails the portability test.
- [x] 1.5 Implement `trash.MoveNoReplace` on Linux with `renameat2(RENAME_NOREPLACE)`, mapping `EINVAL`/`ENOSYS` to `trash.ErrNoReplaceUnsupported`, so `DirTrasher` works. Verify in the container: `TestMoveNoReplace`, `TestDirTrasher`, `TestTrashExpect` and `TestTrashSymlinkIsRefused` pass; a cross-device test between `$TMPDIR` and `/xdev` returns `ErrDifferentDevice` with the source intact (it needs `/xdev`, which the harness always provides); an injected-`EINVAL` unit test covers the unsupported case.
- [x] 1.6 Implement `tag_linux.go` (`user.com.rbutera.kipple`, owner-write dance for read-only objects) and refuse device nodes, FIFOs and sockets before any open. Verify with `internal/apply` fixture tests in the container: `TestReadOnlyDirectoryItem` and "Read-only package folder tagged", "No user extended attributes" (injected `ENOTSUP`, item stays, exit 4), and a FIFO in a fixture cache root refused without blocking.
- [x] 1.7 Implement the `.trashinfo` encoder and decoder: every byte outside the unreserved set and `/` percent-encoded, local `DeletionDate` with no zone, strict parsing that rejects duplicate keys and relative paths for the home Trash. Verify with table tests on any host, including "Unusual bytes in the path" (space, `%`, newline, non-UTF-8) round-tripping.
- [x] 1.8 Implement `trash.FreedesktopTrasher` (design L4): exclusive info reservation, write and `fsync`, `renameat2` into `files/`, Finder-style collision names, orphan handling, `EXDEV` without copy, arrival check with move-back, `DisplacedError` keeping its info file, and removal of only kipple's own info file by inode. Verify with fixture tests in the container "Trash info written", "Name collision in the Trash", "Orphan file without info", a swap-during-move hook test (the displaced entry is recorded and its info kept), and a cross-device test against `/xdev`.
- [x] 1.9 Wire Linux apply in `apply/trash_linux.go`:
  - open `Trash`, `files` and `info` through the session;
  - create missing folders 0700 only with `--apply` and at least one item;
  - check owner and mode;
  - `ProbeTrash` reports without creating anything;
  - `mover: freedesktop` in the plan enum, the receipt schema and the goldens;
  - `ErrNoReplaceUnsupported` becomes `trash_access`.

  In `cmd/internal/launch`, set the Linux state dir to `.local/state/kipple`. Verify with fixture tests "Apply to the freedesktop Trash on Linux", "Missing Trash created on apply", "Dry run creates nothing", "Trash folder is a symlink", "World-writable Trash", "No RENAME_NOREPLACE" (injected), "Linux state directory", "Fixture Trash on Linux" and "Linux receipt validates". The baseline's `internal/apply`, `internal/receipt` and `cmd/kipple` apply tests pass in the container.
- [x] 1.10 Implement `procs_linux.go` over an injectable `fs.FS` (design L7). Verify with synthetic `/proc` trees: "Agent found through its command line on Linux", "Long process name on Linux", "Another user's agent", "/proc unavailable", a cwd ending in ` (deleted)`, and a process vanishing mid-walk. Also run a container test: copy the test binary to `t.TempDir()/claude`, start it with its cwd in a fixture project, and expect `tool_running` (`TestClaudeCodeOpenInProject` on Linux).
- [x] 1.11 Implement restore on Linux (design L5): staging and `RENAME_NOREPLACE` moves inside `files/`, info removal only when `Path` matches and only after the move, and `already_restored` by identity and tag. Verify with fixture tests "Restore removes the info file", "Info file describes something else", "Restore dry run on Linux", "Restored in the file manager first" (simulated as rename back plus info removal), "Trash emptied in the file manager", and every baseline `TestRestore*` and `TestPending*` test passing in the container.
- [x] 1.12 Fix the remaining baseline failures that are not Trash-related: policy and session device-identity tests (`TestSessionOpen`, `TestSessionCacheFollowsChanges`, `TestAncestorSwappedForSymlink`), `TestGuardRealHomeByIdentity`, `internal/sizes` (`TestHardlinkedBytesAreNotReclaimed`, `TestUnreadableSubdirIsPartial`), and the `scan`, `guimodel` and `tui` tests. Fix the cause, typically `Stat_t` field types or the Trash path; tag a test only when its behaviour is genuinely darwin-only, and say why in the file. Verify: `TestEndToEnd`, `TestTUIApply` and `TestTerminalEscapesInTUI` pass in the container, and `make test-linux` is green for every package.
- [x] 1.13 Wave 1 gate: `command make check` on macOS and `make test-linux` on colima are both green, with zero failing and zero platform-skipped tests on Linux. Run `openspec validate linux-port --strict`, then commit.
- [ ] 1.14 Review gate: the orchestrator runs the Opus review and the red-team review of wave 1. The red team focuses on the info-file reservation and cleanup race, restore with forged or mismatched info files, `/proc` spoofing by process title, Trash permission checks, and the harness's real-home isolation. Fix until both pass, then re-run 1.13.

## 2. Wave 2: Linux safety boundaries and content

- [x] 2.1 Add mount-point detection to `internal/policy` (design L3):
  - a mountinfo parser handling octal escapes, optional fields and the `root` field;
  - `statx` `STATX_ATTR_MOUNT_ROOT`, with mountinfo as the fallback, compared by identity;
  - `mounts_unavailable` when neither works.

  Use it in the session walk, `WalkFresh` and the `internal/sizes` walk: `different_device` for candidates and components, `contains_mount` for items. Verify with parser table tests on any host, and fixture tests with an injected mount oracle: "Bind mount inside a cache folder", "Candidate is a bind mount point", "btrfs subvolume under home" (injected device number), and "Mount table unavailable" (scan warns, apply exits 1, fixture checksum unchanged).
- [x] 2.2 Implement the `homeguard` Linux aliases from `/proc/self/mountinfo` (design L2), failing closed when it cannot be read. Verify with stub-FS tests "KIPPLE_HOME through a bind mount of a folder inside home", "KIPPLE_HOME through a bind mount of the home's parent", "Separate temp folder accepted", a btrfs `root=/@home/me` case, and "mountinfo unreadable refuses". Add `cmd/internal/launch` tests that a refused `KIPPLE_HOME` exits 1 before reading the fixture.
- [x] 2.3 Add the Linux denylist entries and the `.Trash-*` component pattern to `internal/policy/denylist.go` (design L9), and regenerate the docs/safety.md denylist. Verify with policy table tests for both `Authorize` and `Session`: "Linux keyring refused", "Trash is never a source", "Folding applies on Linux", one case per new entry, and the existing docs diff test.
- [x] 2.4 Parse `<home>/.config/user-dirs.dirs` in `internal/config` on Linux and add the results to `Env.Protect`:
  - no symlink following;
  - `$HOME/...` and absolute paths under home are accepted;
  - `XDG_DOWNLOAD_DIR` is excluded;
  - a value equal to home or outside it is ignored with a warning;
  - a malformed file warns.

  Verify with fixture tests "Localised Documents folder", "Download dir stays cleanable", "User dir set to home", a malformed file, a symlinked `user-dirs.dirs`, and "protection covers a rule root warns".
- [x] 2.5 Split the catalogue per design L8: an `entry` with per-platform overrides, `CatalogueFor(goos)`, `Catalogue()` using `runtime.GOOS`, the Linux rules with `what`/`if_removed` sentences and owners, `xdg.cache` as `safe` with a 7-day age and the exclude table with one-sentence reasons (design L8), and `kipple explain` refusing other-platform rules with exit 2. Move the golden to `testdata/rules_darwin.json` unchanged and add `testdata/rules_linux.json`. Add a per-platform rule table to `testutil` and switch tests and `scripts/agent-flow-check.sh` that hard-code macOS rule IDs to it. Verify:
  - `TestCatalogueInvariants` runs for both platforms, including the new invariants and "Nested root not excluded";
  - "macOS catalogue unchanged by the port" (byte diff against the v0.2.0 golden);
  - "Shared rule ID", "macOS-only rule on Linux" and "Linux catalogue count";
  - fixture scans in the container: "Old cache folder pre-selected", "Recent cache folder held back", "Excluded folder never offered" (one fixture child per exclude) and "Snap data is info only";
  - "Excludes documented" (docs/safety.md exclude table diffed against the rule).
- [ ] 2.6 Platform plumbing in `cmd/internal/launch` and `cmd/kipple`: `unsupported_platform` for other OSes, XDG warnings (ignored with `KIPPLE_HOME`), home resolution through a symlinked parent, and `os` and `arch` in `kipple version`. Verify with tests "Linux is supported", "Unsupported platform refused" (injected GOOS), "XDG_CACHE_HOME outside home", "XDG_CACHE_HOME elsewhere under home", "Fixture ignores XDG variables", "Symlinked /home" (fixture layout under `t.TempDir()`), "Symlink below home still refused", "Version JSON" and "Platform in version".
- [ ] 2.7 Add `scripts/linux-mount-check.sh` and `make test-linux-mounts`, a privileged throwaway container whose only mount is the read-only repo. If Docker refuses `--privileged`, the target exits non-zero with that reason and never reports a pass. The script builds a fixture under `/tmp/fx` with:
  - a bind mount of `Documents` at `.cache/foo/docs`;
  - a bind mount at `.cache/bar`;
  - a tmpfs at `.cache/baz/t`;
  - an overlay whose lower-layer directory is a cache item;
  - a plain control item.

  As uid 1000 it runs `kipple scan --json` and then `kipple clean --apply --json --only <all of them>`. Verify "Real mounts checked": `.cache/foo` and `.cache/baz` are skipped with `contains_mount`, `.cache/bar` and the overlay item are not moved (`different_device`), only the control item reaches the fixture Trash, nothing is copied, and the fixture's sha256 tree is otherwise unchanged.
- [ ] 2.8 The TUI done view and the CLI human output name the Linux Trash ("your Trash (~/.local/share/Trash), visible in your file manager") and the restore paths. Verify with TUI model and CLI golden tests per platform against fixtures.
- [ ] 2.9 Documentation for waves 1 and 2:
  - docs/safety.md gets "On Linux" subsections: Trash location and `.trashinfo`, file-manager restore, the user tag, folders created 0700, mount boundaries, user-dirs protections, the XDG policy, and known limits (NFS and FUSE, user xattrs, overlayfs, btrfs subvolumes and reflinks, PID namespaces).
  - docs/plan-schema.md documents `mover: freedesktop` and the compatibility rule for enum values and error codes.

  Verify: the denylist diff test passes, and a test validates the doc's Linux receipt example against `kipple schema receipt` ("Documented compatibility rule").
- [ ] 2.10 Wave 2 gate: `command make check`, `make test-linux` and `make test-linux-mounts` are green; commit.
- [ ] 2.11 Review gate: the orchestrator runs the Opus review and the red-team review of wave 2. The red team focuses on mount boundaries (same-device binds, overlay, btrfs), the home guard, the denylist and user-dirs protections, the catalogue split and the XDG policy, and must "find a directory under ~/.cache that is not a cache": every find becomes an `xdg.cache` exclude with a reason and a test before the gate passes. Fix until both pass, then re-run 2.10.

## 3. Wave 3: CI, release, GUI decision, docs, v0.3.0

- [ ] 3.1 Add the `linux` job to `.github/workflows/ci.yml` on `ubuntu-latest`, with actions pinned by SHA. The runner user is not root, and the suite's root guard would fail otherwise. Steps:
  - `make fmt-check`, `golangci-lint` with `GOOS=linux`, `go vet`, `make check-no-real-home`, `go test -race -count=1 ./...`, `go build ./...`;
  - a `CGO_ENABLED=0` build of `cmd/kipple` run through `scripts/agent-flow-check.sh`;
  - `GOCACHE` and `GOMODCACHE` under `$RUNNER_TEMP`, and a `find ~ -xdev` snapshot before and after the tests that fails the job on any change ("Runner home unchanged").

  Verify: `actionlint` passes locally; the orchestrator checks the first run, in which a scratch commit that writes under `$HOME` from a test makes the job fail; and a green run on the branch.
- [ ] 3.2 Add a CI step that runs `make test-linux-mounts` on the Ubuntu runner (privileged container, fixture only). Verify with `actionlint` and the job's output listing the "Real mounts checked" assertions.
- [ ] 3.3 Apply the GUI decision (design L11):
  - `cmd/kipple-gui` exits 1 on Linux before Fyne starts, with a message naming `kipple tui`;
  - the GUI tests are `gui && darwin`, except the refusal test;
  - CI compiles `go build -tags gui ./cmd/kipple-gui` on Ubuntu with `libgl1-mesa-dev xorg-dev` installed.

  Verify: "GUI started on Linux" in CI (`go test -tags gui -run TestLinuxRefusal`), "No Linux GUI artifact" in the snapshot release, and `TestDefaultBuildHasNoFyne` still passing on both OSes.
- [ ] 3.4 Add the `kipple-linux` build and archives to `.goreleaser.yaml`: linux amd64 and arm64, `CGO_ENABLED=0`, `-trimpath`, the same ldflags, and LICENSE, README and docs/safety.md in the archive. They share `checksums.txt` and the cosign signing. Verify: "Linux archives in the signed checksums" via `make release-snapshot`; `file` reports static binaries; and the arm64 tarball's binary, run in the container, prints `os: linux` in `kipple version --json`.
- [ ] 3.5 Update the README:
  - Linux install: tarball with checksum and cosign verification, plus `go install`;
  - a "Linux" section: Trash location, file-manager restore, differences from macOS, NFS and container limits;
  - the platform line in the agent section and the comparison table;
  - a roadmap: Linux GUI in v0.3.1; AUR, then deb, rpm and Nix packaging; top-directory Trash; following `XDG_*` under home; enabling the Maven and JetBrains rules on macOS.

  Update `llms.txt` and `SECURITY.md` too. Verify: "Linux install documented" by running the README's Linux steps against the snapshot tarball in the container, and the agent-flow check passing in the macOS and Ubuntu CI jobs.
- [ ] 3.6 CONTRIBUTING.md: document `make test-linux` (unprivileged; the suite refuses root), `make test-linux-mounts` and its fallback (where Docker cannot run privileged containers the target exits non-zero saying so, and the stub-based mount tests remain the contract; CI always runs it), how to add a rule with per-platform roots, and that both catalogues' invariants must pass. Verify: the existing add-a-rule test passes on both platforms with a scratch rule that has a Linux override.
- [ ] 3.7 Release readiness check:
  - `command make check`, `make test-linux`, `make test-linux-mounts` and `make release-snapshot` are green;
  - `openspec validate linux-port --strict` passes;
  - release notes for v0.3.0 are written: Linux CLI and TUI, the larger denylist, `os`/`arch` in version, `mover: freedesktop`, the GUI still macOS-only.

  Commit, then hand over to the orchestrator to push and tag `v0.3.0` once CI is green on that SHA.
- [ ] 3.8 Review gate: the orchestrator runs the Opus review and the red-team review of wave 3, covering the CI home check, release artifacts and signing, and whether the docs match behaviour. Fix until both pass, then re-run 3.7.
