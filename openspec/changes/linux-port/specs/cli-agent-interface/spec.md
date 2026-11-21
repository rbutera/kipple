# Spec Delta

## MODIFIED Requirements

### Requirement: Version
`kipple version` SHALL print the version, commit, `schema_version`, and the platform as `os/arch`; `--json` SHALL emit them as an object with `version`, `commit`, `schema_version`, `os` and `arch`, where `os` and `arch` use Go's names (`darwin`, `linux`, `amd64`, `arm64`). Adding these fields does not change `schema_version`.

#### Scenario: Version JSON
- **WHEN** `kipple version --json` runs
- **THEN** the object has `version`, `commit` and `schema_version`

#### Scenario: Platform in version
- **WHEN** `kipple version --json` runs on `linux/arm64`
- **THEN** the object has `os: "linux"`, `arch: "arm64"` and `schema_version: 1`

## ADDED Requirements

### Requirement: Additive schema evolution
Within `schema_version` 1, kipple MAY add optional fields, enum values (such as the receipt mover `freedesktop`) and error codes (such as `unsupported_platform`, `mounts_unavailable`); a consumer SHALL be able to rely on no field being renamed, removed or changing meaning without a version bump. `kipple schema <document>` SHALL always describe the running binary's output, including new enum values. Dry run remains the default and the Trash the only destination for every command that can change the filesystem.

#### Scenario: Linux receipt validates
- **WHEN** a receipt written on Linux is validated against `kipple schema receipt` from the same binary
- **THEN** validation passes and `mover` is `freedesktop`

#### Scenario: Documented compatibility rule
- **WHEN** a reader opens docs/plan-schema.md
- **THEN** it states that new fields, enum values and error codes are compatible and that renames, removals and changes of meaning bump `schema_version`
