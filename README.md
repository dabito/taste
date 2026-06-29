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
taste version
```

## Commands

```text
taste check [scope] [--json]   # diagnostics only; no mutation
taste format [scope] [--json]  # format only
taste fix [scope] [--json]     # safe format/fix, then diagnostics
taste gate [scope] [--json]    # completion gate; strict pass/fail
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
```

JSON output is stable for agents:

```bash
taste gate --changed --json
```

## Development

```bash
make test
make vet
make check
go install .
```
