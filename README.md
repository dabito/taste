# taste

`taste` is a small quality CLI for agentic coding workflows.

It gives agents one deterministic command surface for formatting, safe fixes, diagnostics, and strict readiness checks across Go, JavaScript/TypeScript, and Bash.

## Install

```bash
go install github.com/dabito/taste@latest
```

Go installs the binary into `$GOBIN`, or `$GOPATH/bin` when `GOBIN` is unset. Default Go setups usually use:

```text
$HOME/go/bin/taste
```

Ensure Go's bin dir is on `PATH`:

```bash
export PATH="$HOME/go/bin:$PATH"
```

## Usage

```bash
taste main.go
taste main.go scripts/dev.sh --fix
taste . --strict
taste --changed --strict --json
taste --flavors
```

## Command shape

```text
taste [targets...] [--fix|--dry] [--easy|--strict] [--json]
```

Targets are files or directories. Multiple targets allowed. No targets defaults to `--changed` inside a git repo, otherwise `--project` with a warning.

Flags:

```text
--fix        safe autofix, then diagnostics
--dry        diagnostics only; default
--easy       fast/local checks; default
--strict     complete readiness checks
--changed    changed files from git
--project    whole project
--flavors    list diagnostic/check flavors, paths, env overrides, install hints
--stdin-json  read {"paths":[...]} from stdin
```

Go diagnostics use `gopls` when available. JS/TS diagnostics use `typescript-language-server` when available. Bash diagnostics use `bash-language-server` when available. Missing LSPs are warnings with install/override hints; shell/project checks still run.

## Output

Human output is concise:

```text
PASS fixed: none; remaining: 0
checks: gofmt -l:pass, go test:pass, go vet:pass
```

JSON output is stable for agents:

```bash
taste --changed --strict --json
```

## Flavors

A flavor is one diagnostic/check lane, such as `gopls`, `typescript-language-server`, `bash-language-server`, `go test`, or `shellcheck`.

`taste --flavors` reports which flavors are available from the current working directory. It resolves tools through env overrides, repo-local `node_modules/.bin`, then `PATH`.


Common overrides:

```bash
TASTE_GOFMT=/path/to/gofmt
TASTE_GO=/path/to/go
TASTE_NPM=/path/to/npm
TASTE_PRETTIER=/path/to/prettier
TASTE_ESLINT=/path/to/eslint
TASTE_SHELLCHECK=/path/to/shellcheck
TASTE_GOPLS=/path/to/gopls
TASTE_TYPESCRIPT_LANGUAGE_SERVER=/path/to/typescript-language-server
TASTE_BASH_LANGUAGE_SERVER=/path/to/bash-language-server
```

## Development

```bash
make test
make vet
make check
go install .
```
