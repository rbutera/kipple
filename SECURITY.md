# Security policy

kipple moves files out of your home folder, so a path that escapes its
allowlist or denylist is a security bug, not just a correctness bug.

## Reporting a vulnerability

Please report privately through GitHub's private vulnerability reporting:
open the repository's **Security** tab and choose **Report a vulnerability**.
Do not open a public issue for anything that could cause data loss.

Useful details:

- the kipple version (`kipple version --json`) and macOS version
- the rule ID and the path involved (redact personal names if you like)
- how the item was moved (the receipt's `mover`: `rename` into `~/.Trash`,
  or `nsfilemanager` for Apple's Trash API)
- the receipt file from the state directory, if one was written

You should get an acknowledgement within seven days. Fixes for paths that
escape the policy are released as soon as they are ready, with a note in the
release changelog.

## Supported versions

Only the latest release receives fixes until v1.0.0.

## Scope

In scope: anything that lets kipple list, move, delete or restore a path the
policy should refuse (denylisted paths, symlink traversal, paths outside home,
paths on another volume), running as root, or network access.

Out of scope: disk space that kipple does not reclaim. Refusing to touch
something is the intended failure mode.
