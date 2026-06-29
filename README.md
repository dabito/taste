# taste

`taste` is a small quality-gate CLI for agentic coding workflows.

It gives agents one deterministic command surface for formatting, safe fixes, diagnostics, and completion gates across Go, JavaScript/TypeScript, and Bash.

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
taste check --changed
taste format --paths main.go scripts/dev.sh
taste fix --changed --json
taste gate --project
taste flavors list
taste doctor
taste version
```

## Commands

```text
taste check [scope] [--json]   # diagnostics only; no mutation
taste format [scope] [--json]  # format only
taste fix [scope] [--json]     # safe format/fix, then diagnostics
taste gate [scope] [--json]    # completion gate; strict pass/fail
taste flavors list [--json]    # list diagnostic/check flavors, paths, env overrides, install hints
taste doctor [--json]          # alias for flavors list
taste version
```

Scopes:

```text
--changed            changed files from git
--project            whole project
--paths <files...>   explicit files
--stdin-json         read {"paths":[...]} from stdin
```

Default scope is `--changed` inside a git repo, otherwise `--project` with a warning.

## Output

Human output is concise:

```text
PASS fixed: go format 3 files; remaining: 0
checks: gofmt:pass, go test:pass, go vet:pass
```

JSON output is stable for agents:

```bash
taste gate --changed --json
```

## Flavors

A flavor is one diagnostic/check lane, such as `gopls`, `typescript-language-server`, `bash-language-server`, `go test`, or `shellcheck`.

`taste flavors list` reports which flavors are available from the current working directory. It resolves tools through env overrides, repo-local `node_modules/.bin`, then `PATH`.

Future commands may add/update project flavors:

```text
taste flavors add <name>
taste flavors update
```

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
