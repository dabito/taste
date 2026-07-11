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
taste main.go scripts/dev.sh --autofix
taste . --strict
taste --changed --strict --json
taste --flavors
taste --version
```

## Command shape

```text
taste [targets...] [--autofix|--dry] [--easy|--strict] [--json] [--max-issues N]
```

Targets are files or directories. Multiple targets allowed. Use `--` before targets that begin with `-`. No targets defaults to `--changed` inside a git repo, otherwise `--project` with a warning.

Flags:

```text
--autofix         best-effort autofix; not everything is fixable (see below); does not diagnose (run again without --autofix to check)
--dry             diagnostics only; default
--easy            fast/local checks; default
--strict          complete readiness checks
--changed         staged+unstaged tracked changes vs HEAD, plus untracked supported files
--project         whole project
--flavors         list diagnostic/check flavors, paths, env overrides, install hints
--stdin-json      read {"paths":[...]} from stdin
--max-issues N    cap JSON issues; default 200
```

Go diagnostics use `gopls` when available. JS/TS diagnostics use `typescript-language-server` when available. Bash diagnostics use `bash-language-server` when available. Missing LSPs are warnings with install/override hints; shell/project checks still run.

### `--autofix` is best-effort

Not every diagnostic has a mechanical fix -- an unused variable, for example,
needs a human decision, not a rewrite. `--autofix` only runs the tools a
flavor actually has a fix step for (e.g. `gofmt -w`); it does not diagnose
afterward to confirm nothing's left (that's a second, explicit call). If a
flavor's diagnostic tools include one with no fix step, `--autofix` says so
in `warnings`, naming the tool, so "only some of my issues got fixed" is
never a silent surprise.

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

With `--json`, stdout is exactly one JSON document. Human logs/errors go to stderr. Exit codes:

```text
0    pass
1    fail: confirmed diagnostic issues
2    usage/config error
3    incomplete: a required diagnostic tool for files in scope couldn't run
     (missing, crashed, or timed out) and no issues were otherwise found --
     distinct from fail: this means readiness couldn't be verified, not
     that a problem was confirmed
4-9  reserved
10+  internal error
```

`status` mirrors this: `"pass" | "fail" | "incomplete"`. A run with both real issues and an unavailable tool is `fail` -- confirmed problems take priority over unverified ones.

The JSON includes `schema_version`, `status`, `scope`, `level`, `summary`, `checks`, `fixed`, `issues`, `total_issues`, `warnings`, `commands`, and `incomplete`. `--max-issues` caps `issues`; `total_issues` reports the pre-cap count.

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

### Customizing flavors

The go/javascript/bash flavors are defined in a TOML registry, built into the
binary from [`flavors.default.toml`](flavors.default.toml). To customize a
flavor (add a tool, change args, add a new language), copy that file and
place it at one of these paths -- `taste` merges it whole-flavor-by-name over
the built-in default:

```bash
cp flavors.default.toml .taste/flavors.toml          # project-local
cp flavors.default.toml ~/.config/taste/flavors.toml # user-level
```

Edit the copy; you don't need to redefine every flavor, just the ones you're
changing. See [`FLAVORS_PROPOSAL.md`](FLAVORS_PROPOSAL.md) for the full
vocabulary (`tool`, `action`, `step`, `kind`).

## Development

```bash
make test
make vet
make check
go install .
```
