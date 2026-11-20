## What and why

<!-- What does this change, and why? Link the issue if there is one. -->

## Safety checklist

- [ ] **No test, script or manual check touches a real home folder.** Every file-system test builds a fixture with `testutil.NewHome` (under `t.TempDir()`, with `KIPPLE_HOME` and `KIPPLE_STATE_DIR` set), and nothing was run with `--apply` outside a fixture.
- [ ] Does any test touch the real home? **No** / Yes (explain; this blocks the merge)
- [ ] `internal/policy.Authorize` is still the only thing that approves a move; no new code path decides a path is deletable, and nothing follows a symbolic link.
- [ ] Dry run is still the only default and the Trash the only destination.
- [ ] New or changed rules carry `What` and `IfRemoved` sentences and a scan test (see "Adding a rule" in CONTRIBUTING.md).
- [ ] `command make check` passes locally.

## Notes for review

<!-- Anything a reviewer should look at closely: changes to policy, apply, trash, receipts or the catalogue. -->
