# Proposal: config-driven flavor registry

## Status

Draft, not yet implemented. Captures the design discussed after a multi-focal
critique of `taste` (correctness/robustness, security, testing/packaging,
architecture/agent-contract). See "Findings this resolves" below for the
specific defects this design closes.

## Problem

Adding a language today touches five places in `main.go`/`lsp.go`:

1. an `isXFile` extension classifier
2. a fixed field on the `fileGroups` struct
3. a branch in `classifyFiles`
4. a branch in `runTaste`
5. a hand-written `runX` function re-implementing the same
   format-if-fix -> diagnose (LSP if available) -> strict-tier-extras shape
   with different concrete tools plugged in

`allToolDefs()` is already data-shaped (`toolDef{Name, Language, Env, Install,
LocalNPM}`) but only feeds tool *resolution* and `--flavors` — it does not
drive dispatch. The LSP half of the problem is already solved generically:
`runLSPDiagnostics(lspRunConfig)` in `lsp.go` is a real engine; the three
`runXDiagnostics` wrappers are thin adapters over it. This proposal extends
that pattern to the parts that are not yet generic.

## Goal

Replace the five hardcoded touch points with one data-driven flavor registry,
loaded from TOML, with a single generic dispatcher reading it. Adding a
language becomes "add a `[[flavor]]` entry," not "add a Go function."

## Config shape

```toml
[[flavor]]
name = "go"
extensions = [".go"]
root_markers = ["go.mod"]

  [[flavor.tool]]
  name = "gofmt"
  env = "TASTE_GOFMT"
  install = "ships with Go"

  [[flavor.tool]]
  name = "go"
  env = "TASTE_GO"
  install = "install Go from https://go.dev/dl/"

  [[flavor.tool]]
  name = "gopls"
  kind = "lsp"
  env = "TASTE_GOPLS"
  install = "go install golang.org/x/tools/gopls@latest"
  issue_language = "go"

  [flavor.actions.fix]
  steps = [
    { tool = "gofmt", kind = "argv", args = ["-w", "{files}"], per_file = false, fixable = true },
  ]

  [flavor.actions.taste]
  steps = [
    { tool = "gopls", kind = "lsp" },
    { tool = "gofmt", kind = "argv", name = "gofmt -l", args = ["-l", "{files}"], per_file = false, fixable = true },
  ]

  [flavor.actions.strict]
  requires_root_marker = true
  steps = [
    { tool = "go", kind = "argv", name = "go test", args = ["test", "./..."] },
    { tool = "go", kind = "argv", name = "go vet",  args = ["vet", "./..."] },
  ]

[[flavor]]
name = "javascript"
extensions = [".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts"]
root_markers = ["package.json"]

  [[flavor.tool]]
  name = "typescript-language-server"
  kind = "lsp"
  args = ["--stdio"]
  issue_language = "javascript"
  install = "npm install -D typescript-language-server typescript"
  local_npm = true

  [flavor.actions.fix]
  steps = [
    { tool = "npm", kind = "npm_script", script = "format", fixable = true, requires_confirmation = true },
  ]

  [flavor.actions.taste]
  steps = [
    { tool = "typescript-language-server", kind = "lsp" },
    { tool = "npm", kind = "npm_script", script = "lint", requires_confirmation = true },
  ]

  [flavor.actions.strict]
  requires_script = "test"
  steps = [
    { tool = "npm", kind = "npm_script", script = "test", requires_confirmation = true },
  ]

[[flavor]]
name = "bash"
extensions = [".sh", ".bash", ".zsh"]

  [[flavor.tool]]
  name = "bash-language-server"
  kind = "lsp"
  args = ["start"]
  issue_language = "bash"
  install = "npm install -D bash-language-server"
  local_npm = true

  [[flavor.tool]]
  name = "shellcheck"
  install = "install shellcheck"

  [flavor.actions.taste]
  steps = [
    { tool = "bash-language-server", kind = "lsp" },
    { tool = "bash", kind = "argv", name = "bash -n", args = ["-n", "{file}"], per_file = true },
  ]

  [flavor.actions.strict]
  steps = [
    { tool = "shellcheck", kind = "argv", args = ["{file}"], per_file = true },
  ]
```

### Step fields

```text
tool                   references a [[flavor.tool]] name
kind                    "lsp" | "argv" | "npm_script"
name                    display name for commands[]; defaults to tool name
args                    argv template, "argv" kind only
script                  package.json script name, "npm_script" kind only
per_file                required (no default) for "argv" steps; batches vs loops per file
fixable                 marks any issue this step produces as agent-fixable
requires_confirmation   gate behind --allow-scripts / TASTE_ALLOW_SCRIPTS; default false,
                        true on all built-in npm_script steps
```

`per_file` has no default and must be stated explicitly for every custom
`argv` step. taste cannot safely infer whether an arbitrary tool accepts
multiple paths in one invocation — a nonzero exit code can't distinguish "a
real issue in one of N files" from "this tool doesn't support batching." For
built-in flavors this is a one-time correct answer from experience (gofmt
batches; `bash -n`/`shellcheck` don't). For custom flavors it's a required
field, not a guess.

### Action-level readiness gates

`requires_root_marker` (Go) and `requires_script` (JS) replace the current
ad hoc `if fileExists(...)`/`if scripts["test"]` checks scattered in
`runGo`/`runJS`. When a gate fails, the action is skipped with an explicit
warning and a `status: "skip"` entry in `commands` — never silently treated
as pass, and never silently omitted.

### Trust gating for repo-declared execution

`npm_script` steps run whatever the target repo's `package.json` declares —
this is arbitrary code execution by design, not a fixed known tool like
gofmt. Every built-in `npm_script` step defaults `requires_confirmation =
true`. A gated step only runs if the caller passes `--allow-scripts` or sets
`TASTE_ALLOW_SCRIPTS=1`; otherwise it's skipped with a warning naming the
exact command that was withheld. taste is primarily invoked by unattended
agents, so the gate refuses by default rather than prompting interactively.
This gives `pi-taste` (or any wrapper) a real trust decision to make instead
of none: e.g. pass `--allow-scripts` for `changed` scope (the user is already
working in this repo) but withhold it for a cold `--project` run against a
repo nobody has touched yet.

### Failure diagnostics for custom steps

Built-in steps keep today's 3-line `summarizeOutput` truncation. Any step
from a non-embedded (user/project) flavor shows full argv plus untruncated
stderr on failure, and if `per_file = false` and the process fails on a
multi-path invocation, appends a fixed hint: `"if this tool doesn't accept
multiple paths, set per_file = true"`.

## Config resolution

Closest-wins layering, no partial merge:

1. Walk up from cwd for a project-local `.taste/flavors.toml` (stop at
   repo root or filesystem root).
2. Fall back to `$XDG_CONFIG_HOME/taste/flavors.toml`, or
   `~/.config/taste/flavors.toml` if unset.
3. Fall back to the built-in default, embedded via `go:embed` so a fresh
   `go install` always has working go/js/bash flavors with zero files on
   disk — the zero-config default must never regress.

A user/project file replaces a built-in flavor **whole, by name** — no deep
field merging. A name not present in the built-in set is a pure addition.
This avoids "which layer set this field" ambiguity at the cost of forcing a
full redefinition to change one field of a built-in flavor; acceptable for
v1.

### Unmatched file types

The embedded default must keep working standalone for go/js/bash with no
config file present. The nudge is scoped narrowly: if `taste` encounters a
file extension no loaded flavor (built-in or user) claims, it warns once:
`"no flavor matches .py files; add one at ~/.config/taste/flavors.toml"`.

## Findings this resolves

From the multi-focal critique:

- **#4** (unconditional `npm run <script>` execution, high-severity security
  finding) — closed by `requires_confirmation` + `--allow-scripts` gating.
- **#11** (macOS-specific `brew install` hints) — closed by writing
  OS-neutral install strings in the shipped default (`"install shellcheck"`).
- **#16** (`nextAction()` guessing fixability via string-matching in
  pi-taste) — closed by `fixable` on steps propagating to `issueItem`,
  letting the wrapper check a field instead of pattern-matching text.
- **#17** (ad hoc, inconsistent per-language strict-readiness gates) —
  closed by `requires_root_marker`/`requires_script` as an explicit,
  declared action-level precondition.
- **#18** (`allToolDefs()` data shape unused by dispatch) — closed by making
  the whole dispatcher read this table; `allToolDefs()` disappears, replaced
  by `flavor.tool` entries.

Not addressed by this proposal (tracked separately in the roadmap):

- **#1/#2/#3** (false-PASS risk from LSP deadline / demoted tool errors /
  git error handling) — P0, orthogonal to config shape, must land first.
- **#13/#14/#15** (checks/commands schema split, `column` never populated,
  `schema_version` decorative, pi-taste's duplicate sort/cap) — P1 schema
  reconciliation; this registry's `commands`/`checks` output should be
  built on the *unified* schema, so sequencing schema work before or
  alongside this registry avoids building the dispatcher against a shape
  that's about to change.

## Open questions

1. Exact TOML key for the templating placeholders (`{files}`/`{file}`) —
   proposal above; confirm no richer placeholder (e.g. `{root}`) is needed
   before implementation.
2. Whether `requires_confirmation` should also apply to any future
   non-`npm_script` step that runs repo-declared code (e.g. a hypothetical
   `make`-target step) — likely yes, by the same reasoning, but no such step
   exists yet to design against.
3. Whether project-local `.taste/flavors.toml` should be trusted
   automatically or itself require confirmation the first time it's seen in
   a freshly cloned repo — a config file is itself repo-declared content
   that changes what commands run.
