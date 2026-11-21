# cli-agent-interface Specification

## Purpose

The CLI is designed to be driven by agents as well as people: machine-readable output, strict stream discipline, documented exit codes, and no hidden prompts.

## Requirements

### Requirement: JSON on every command
Every command SHALL accept `--json`. With `--json`, stdout SHALL contain exactly one JSON document and nothing else; progress, warnings and errors go to stderr. Errors with `--json` SHALL also be reported as a JSON object on stdout with `error.code` and `error.message`.

#### Scenario: Scan JSON is parseable
- **WHEN** `kipple scan --json` output is piped to a JSON parser
- **THEN** it parses as one document

### Requirement: Published schemas
`kipple schema plan|receipt|rules|result` SHALL print the JSON Schema for that document, generated from the same types that encode it, including `schema_version`.

#### Scenario: Plan validates against schema
- **WHEN** a plan from `kipple scan --json` is validated against `kipple schema plan`
- **THEN** validation passes

### Requirement: Exit codes
All commands SHALL use: 0 success, 1 error, 2 usage error, 3 nothing to do, 4 partial (some items skipped or failed). `kipple --help` SHALL document them.

#### Scenario: Unknown flag
- **WHEN** `kipple scan --bogus` runs
- **THEN** exit code is 2

### Requirement: No prompts off-TTY
When stdout is not a TTY or `--json` is set, no command SHALL wait for input.

#### Scenario: Non-TTY clean
- **WHEN** `kipple clean --apply` runs with stdout redirected to a file
- **THEN** it completes without reading stdin

### Requirement: Explain
`kipple explain <item-id|rule-id>` SHALL print the rule's what/if_removed text, tier, roots, and for an item its path, size and reasons.

#### Scenario: Explain a rule
- **WHEN** `kipple explain xcode.deriveddata` runs
- **THEN** output includes the tier, the roots and both explanation sentences

### Requirement: Version
`kipple version` SHALL print the version, commit, `schema_version`, and the platform as `os/arch`; `--json` SHALL emit them as an object with `version`, `commit`, `schema_version`, `os` and `arch`, where `os` and `arch` use Go's names (`darwin`, `linux`, `amd64`, `arm64`). Adding these fields does not change `schema_version`.

#### Scenario: Version JSON
- **WHEN** `kipple version --json` runs
- **THEN** the object has `version`, `commit` and `schema_version`

#### Scenario: Platform in version
- **WHEN** `kipple version --json` runs on `linux/arm64`
- **THEN** the object has `os: "linux"`, `arch: "arm64"` and `schema_version: 1`

### Requirement: Test home override
The environment variable `KIPPLE_HOME` SHALL override the home directory and `KIPPLE_STATE_DIR` SHALL override the state directory, so tests and agents can target fixture trees. When `KIPPLE_HOME` is set, every output SHALL include the effective home.

#### Scenario: Fixture home
- **WHEN** `KIPPLE_HOME=/tmp/fx kipple scan --json` runs
- **THEN** the plan's `home` is `/tmp/fx` and no path outside it appears

### Requirement: Agent documentation
The README SHALL contain an "For agents" section with the scan, inspect, subset-apply, and restore flow, the exit codes, and the schema command, and the repo SHALL ship an `llms.txt` with the same.

#### Scenario: Documented flow runs
- **WHEN** the commands in the README agent section are run against a fixture in CI
- **THEN** they exit with the documented codes

### Requirement: Additive schema evolution
Within `schema_version` 1, kipple MAY add optional fields, enum values (such as the receipt mover `freedesktop`) and error codes (such as `unsupported_platform`, `mounts_unavailable`); a consumer SHALL be able to rely on no field being renamed, removed or changing meaning without a version bump. `kipple schema <document>` SHALL always describe the running binary's output, including new enum values. Dry run remains the default and the Trash the only destination for every command that can change the filesystem.

#### Scenario: Linux receipt validates
- **WHEN** a receipt written on Linux is validated against `kipple schema receipt` from the same binary
- **THEN** validation passes and `mover` is `freedesktop`

#### Scenario: Documented compatibility rule
- **WHEN** a reader opens docs/plan-schema.md
- **THEN** it states that new fields, enum values and error codes are compatible and that renames, removals and changes of meaning bump `schema_version`
