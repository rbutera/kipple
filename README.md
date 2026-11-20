<p align="center"><img src="assets/brand/readme-header.svg" alt="kipple: clears the junk your AI tools and dev toolchains leave behind" width="100%"></p>

# kipple

kipple is an open-source (MIT) macOS disk cleaner for the junk that AI coding
tools, local models and developer toolchains leave behind in your home folder.

> Status: pre-release. Nothing is installable yet. The full README (install,
> usage, the "For agents" section, exit codes and schemas) lands with v0.1.0.

## The safety model in one paragraph

Dry run is the only default: nothing moves without `--apply`. The Trash is the
only default destination, and every move writes a receipt you can restore from.
Every path must descend from an allowlisted rule and pass a single policy check
with a hard-coded denylist (Documents, Desktop, Photos, Mail, keychains, SSH keys,
agent memory and more). Symlinks are never followed. kipple stays inside your home
folder and refuses to run as root.

macOS lets only apps with Full Disk Access open `~/.Trash`. Without it,
kipple uses the system's own Trash call instead; if that is refused too,
`clean --apply` stops with error `trash_access`: grant Full Disk Access to your
terminal in System Settings > Privacy & Security > Full Disk Access. See
[docs/safety.md](docs/safety.md).

## Licence

MIT. See [LICENSE](LICENSE).
