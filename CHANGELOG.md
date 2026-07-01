# Changelog

## Unreleased

- Add JSON `schema_version` and `total_issues` fields.
- Add `--max-issues` to cap JSON issues while preserving pre-cap count.
- Add `--` separator support for targets that begin with `-`.
- Define `--changed` as staged+unstaged tracked changes vs `HEAD`, plus untracked supported files.
- Reject `--project --fix` in v0 to avoid unbounded mass mutation.
- Document JSON stdout discipline and exit codes.

Initial public beta candidate.

- Add target-first CLI: `taste [targets...] [--fix|--dry] [--easy|--strict]`.
- Add flavor discovery via `taste --flavors`.
- Add LSP-first diagnostics for Go, JavaScript/TypeScript, and Bash when language servers are available.
- Add shell/project fallback checks: `gofmt`, `go test`, `go vet`, npm scripts, `bash -n`, and `shellcheck`.
- Add fixture-based diagnostic tests for bad Go, TypeScript, and Bash files.
