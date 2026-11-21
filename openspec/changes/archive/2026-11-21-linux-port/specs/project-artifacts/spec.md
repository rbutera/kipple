# Spec Delta

## MODIFIED Requirements

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
