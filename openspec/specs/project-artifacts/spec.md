# project-artifacts Specification

## Purpose

Project artifact rules find rebuildable build output inside idle projects and only ever offer it when the project is provably safe to touch.

## Requirements

### Requirement: Marker adjacency
A project artifact SHALL be offered only if the artifact directory sits directly beside its tool's manifest: `node_modules` beside `package.json`, `target` beside `Cargo.toml`, `.venv` or `venv` beside `pyproject.toml` or `requirements.txt`, `.next` beside `next.config.*`.

#### Scenario: Folder merely named target
- **WHEN** a directory `target` has no sibling `Cargo.toml`
- **THEN** it is not listed

### Requirement: Project roots
Project scanning SHALL look only under configured project roots (default: whichever of `~/dev`, `~/code`, `~/projects`, `~/src`, `~/Developer`, `~/repos` exist), to a bounded depth, and SHALL NOT descend into artifact directories or denylisted paths.

#### Scenario: Project outside roots
- **WHEN** a project lives in `~/Desktop/app`
- **THEN** its artifacts are never listed

### Requirement: Idle condition
A project is idle only if no file outside its artifact directories was modified in the last 30 days. Non-idle projects SHALL be listed as `review` items that are not selected, with reason `project_active`.

#### Scenario: Recently edited
- **WHEN** a source file in the project was modified 2 days ago
- **THEN** the artifact item has `selected: false` and reason `project_active`

### Requirement: Git condition
A project artifact SHALL be selectable only when the repository state is `clean_pushed` (no uncommitted changes, no unpushed commits, upstream configured). States `dirty`, `unpushed`, `no_upstream`, `not_a_repo`, `git_unavailable` SHALL leave the item unselected with that state as the reason and SHALL mark the item `at_risk: true` for `dirty` and `unpushed`.

#### Scenario: Unpushed commits
- **WHEN** the project has one commit ahead of its upstream
- **THEN** the item is listed, unselected, with `git.state: unpushed` and `at_risk: true`

#### Scenario: No git
- **WHEN** the `git` binary is not on PATH
- **THEN** every project item has `git.state: git_unavailable` and is unselected

### Requirement: Re-check before move
Apply SHALL re-evaluate the idle and git conditions immediately before moving each project artifact and SHALL skip the item if either no longer holds.

#### Scenario: Agent writes to project mid-run
- **WHEN** a file in the project is created after scan and before the move
- **THEN** the item is skipped with reason `project_active` or `git_dirty`

### Requirement: Running tool check
Apply SHALL skip a project artifact if a known agent process (`claude`, `codex`, `cursor-agent`) has the project directory as its working directory, with reason `tool_running`. On macOS processes are found with the system's process tools. On Linux they are found by reading `/proc`: a process matches a name when its `comm` equals it, or when the base name of its first command-line argument equals it (which covers names longer than the kernel's 15-byte `comm` and Node programs that set their process title), and its working directory is read from `/proc/<pid>/cwd`. The same matching applies to the owner-process checks of other rules. Only the user's own processes are considered. When processes cannot be read, the answer is unknown and the artifact is skipped with reason `tool_running`. Dry run is the default and the Trash the only destination; this check runs immediately before each move.

#### Scenario: Claude Code open in project
- **WHEN** a `claude` process has cwd inside the project
- **THEN** the item is skipped with reason `tool_running`

#### Scenario: Agent found through its command line on Linux
- **WHEN** on Linux a process whose `comm` is `node` has the first command-line argument `claude` and its cwd inside the project
- **THEN** the item is skipped with reason `tool_running`

#### Scenario: Long process name on Linux
- **WHEN** on Linux kipple checks for a process named `com.docker.backend` (an owner process), whose `comm` is the truncated `com.docker.back`, and its first command-line argument ends in `com.docker.backend`
- **THEN** the process counts as running, and a process whose `comm` is `com.docker.back` with a different command line does not

#### Scenario: Another user's agent
- **WHEN** on Linux a `claude` process owned by another user has its cwd inside the project
- **THEN** it is not considered, and the item's other conditions decide

#### Scenario: /proc unavailable
- **WHEN** on Linux `/proc` cannot be read
- **THEN** the project artifact is skipped with reason `tool_running` and the detail says processes could not be checked
