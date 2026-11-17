# kipple: conventions for agents working in this repo

kipple is an MIT macOS disk cleaner for AI-tool and developer junk. Go module `github.com/rbutera/kipple`. One core library under `internal/`, three front ends under `cmd/` (CLI with `tui` subcommand, optional `kipple-gui` behind `//go:build gui`). Read `openspec/changes/kipple-v1/{proposal,design}.md` and the spec deltas under `openspec/changes/kipple-v1/specs/` before writing code; they are the contract.

## Hard rules (these are not suggestions)

1. **Never touch the real home folder.** No test, benchmark, script, Makefile target or manual check may create, modify, move or delete anything under the real `$HOME` of the machine it runs on. Every filesystem test builds a fixture home with `internal/testutil.NewHome` in `t.TempDir()` and sets `KIPPLE_HOME` and `KIPPLE_STATE_DIR` to it. Read-only inspection of real paths (`ls`, `stat`) is fine. If you need to see what a real cache dir looks like, look, do not run kipple against it. This machine is shared with live agents and its disk is nearly full.
2. **Never run `kipple clean --apply`, `kipple restore --apply`, or the TUI/GUI apply action against anything but a fixture.** Not even once "to check it works".
3. **`internal/policy.Authorize` is the only thing that may approve a move.** Scan calls it to decide what is listable; apply calls it again immediately before each move. Never add a second code path that decides a path is deletable. Never follow a symlink anywhere.
4. **Dry run is the only default. Trash is the only default destination.** Any command that can change the filesystem must do nothing without `--apply`.
5. **The denylist in `internal/policy` is a Go constant.** Config may add protections, never remove them.
6. **The library never reads `os.UserHomeDir()`.** Only `cmd/` resolves home, from `KIPPLE_HOME` or the real home, and injects it. `make check-no-real-home` enforces this.
7. **JSON discipline.** With `--json`, stdout carries exactly one JSON document; everything else goes to stderr. Exit codes: 0 ok, 1 error, 2 usage, 3 nothing to do, 4 partial.
8. **No network, no telemetry, no sudo, no running as root.**

## Working style

- Go 1.23, standard layout, `gofmt` and `golangci-lint` clean, `go vet` clean, `go test -race ./...` green. `make check` runs all of it; it must pass before you commit a wave.
- Table-driven tests; one test case per spec scenario, named after it. Golden files live beside the test in `testdata/`.
- Every rule in `internal/rules` carries `What` and `IfRemoved` sentences written for a non-expert.
- Small commits with descriptive messages after each logical unit. Do not push; the orchestrator pushes.
- Dependencies: cobra, Bubble Tea + bubbles + lipgloss, `invopop/jsonschema`, `santhosh-tekuri/jsonschema` (tests), `BurntSushi/toml`, Fyne (gui tag only). Ask before adding anything else.
- Do not spawn review or verification subagents. Implement, run the gates, commit, report facts. The orchestrator owns review.
