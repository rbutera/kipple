# Red-team recommendations for Wave 3 apply (from wave 1 review)

- Call Authorize immediately before each move with the same Env and Protect list; re-check match shape and conditions (age, idle, clean git, owner running) at move time.
- Close the check-then-move race with fds: open home O_DIRECTORY|O_NOFOLLOW; openat each component O_NOFOLLOW|O_DIRECTORY, fstat each for dev; renameat(parentfd, name, trashfd, newname), renameatx_np RENAME_EXCL on macOS. Plain os.Rename follows symlinks in intermediate components. Needs golang.org/x/sys/unix (approved).
- Treat the Trash as a target to check: lstat ~/.Trash, not a symlink, same device as the item. On EXDEV never fall back to copy+delete.
- Size walks, permanent delete, restore walks must never follow symlinks and must stop at an st_dev change (volume mounted inside a cache folder).
- Hardlinks: moving is safe; do not count bytes with st_nlink > 1 as reclaimed.
- git for RequiresCleanGit: run with `-c core.fsmonitor= -c core.hooksPath=/dev/null` so repo-local hooks never execute.
- Restore must Authorize the destination path from the receipt with the same name-identity checks; receipts are untrusted input.
- EPERM/EACCES from SIP/TCC-protected caches = partial (exit 4), never elevate.
- Wave 2 scan: flag denylisted descendants (.env, *.keychain*, etc.) inside an item during the size walk and refuse the item (reason `contains_protected`), or at minimum report it.

# Added after wave 2 red team
- Object, not name: immediately before renameat, fcntl(F_GETPATH) on the verified parent fd, re-run lexical+identity checks on that path, refuse if it differs from the planned parent. Use renameatx_np RENAME_EXCL into a verified Trash fd on the same device; never copy+delete on EXDEV.
- Apply: fresh Session walk with no cache (Session.WalkFresh), re-measure with guard, refuse items now partial / contains_mount / contains_protected.
- Plan file is untrusted: match by ID, re-derive the path from the rule, re-authorise; never act on a path a fresh scan would not produce. Refuse if item (dev, ino) differs from the plan.
- Refuse if rule root (dev, ino) differs from scan time (record root identity in plan.rules[]).
- Owner-running check (lsappinfo / pgrep) is NOT implemented in wave 2; wave 3 must add it before clean --apply exists.
- Partial items exit 0 on scan (spec); apply with skipped items exits 4.
