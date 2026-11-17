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

## Licence

MIT. See [LICENSE](LICENSE).
