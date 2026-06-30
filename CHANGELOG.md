# Changelog

## v0.1.0-beta.1

Initial public beta candidate.

- Add target-first CLI: `taste [targets...] [--fix|--dry] [--easy|--strict]`.
- Add flavor discovery via `taste --flavors`.
- Add LSP-first diagnostics for Go, JavaScript/TypeScript, and Bash when language servers are available.
- Add shell/project fallback checks: `gofmt`, `go test`, `go vet`, npm scripts, `bash -n`, and `shellcheck`.
- Add fixture-based diagnostic tests for bad Go, TypeScript, and Bash files.
