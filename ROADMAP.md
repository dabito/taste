# Roadmap

Source: a multi-focal critique (correctness/robustness, security,
testing/packaging, architecture/agent-contract) run against the codebase as
of 2026-07. Findings are numbered for cross-reference from proposal docs
(see `FLAVORS_PROPOSAL.md` for the P2 item in full detail). Item text below
is left as originally written (the finding, not a running log); status is
tracked here instead of edited into each item.

## Status

- **P0 (1-4): done.** LSP deadline hit now errors into `incomplete` instead
  of silently returning zero issues, and the wait is configurable
  (`TASTE_LSP_TIMEOUT`, longer default for `--strict`). Missing/crashed
  tools push status off a clean pass via `incomplete`. Git-state fallback
  covers detached-HEAD/empty-repo. `--allow-scripts` gates npm script
  execution.
- **P1 CI (5-6): done.** `.github/workflows/ci.yml`, macOS+Ubuntu matrix,
  real toolchains, fails the build on any skipped test.
- **P1 schema reconciliation (10-13): done.**
- **P2 flavor registry: done.** See `flavors.go`/`dispatch.go`/
  `flavors.default.toml`.
- **Item 7 (version-sensitive assertions): done.** Fixture assertions now
  key on each tool's own stable structured code (gopls/go-types
  diagnostic code, TS's numeric code) and only check message
  non-emptiness, not exact prose wording.
- **Item 8, item 9, the rest of P3: not started.** Install hints still
  assume Homebrew even on Linux (Windows explicitly out of scope, not a
  gap), no ldflags-injected `version`, no subprocess-level e2e smoke test.
  Lower-severity P0 security notes (silent env-override signal, no path
  containment check) also still open.
- **P4 (`taste-mcp`): scoped, not started.** Full design in `pi-taste`'s
  `TASTE_MCP_PROPOSAL.md` -- an npm workspace (`@taste/core` +
  `pi-taste` + `taste-mcp`), one `SURFACE_CONTRACT.md`, Zod schemas for
  the MCP side, and a rollout order that adds the first real unit tests
  this logic has ever had.

## P0 — stop reporting false PASS

The whole point of `taste` is telling an agent whether it's safe to mark
work done. Right now it can say yes when it should say no.

1. **LSP diagnostics deadline swallows late results, not just slow ones**
   (`lsp.go:199-227`). The wait-for-`publishDiagnostics` loop hits a hard 3s
   deadline and proceeds regardless of whether every file actually reported
   in. A file whose diagnostics haven't arrived yet (large file, cold
   package load, slow tsserver project analysis) silently gets **zero
   issues** — not a warning, not an error. Fix: either make the deadline
   generous enough to be a real timeout (configurable, tied to
   `--strict`'s longer overall timeout) and/or surface a warning + partial
   result marker when it's hit, instead of treating "ran out of time" the
   same as "found nothing."

2. **Missing/crashed/timed-out LSP tools are demoted to warnings that never
   affect `status`** (`main.go:856-858`, `899`, `973`; `finalize`,
   `main.go:546-554`, only fails on `TotalIssues > 0`). This directly
   contradicts the PRD's own invariant: *"Missing required strict lanes
   should not create false green readiness."* Fix: a missing/failed
   required-for-the-requested-level tool should push `status` to `fail` (or
   a distinct `incomplete`, per the PRD's still-open question #4) — not get
   buried in `warnings` while `status: "pass"` ships.

3. **Git error handling has an inconsistent fallback.** "Not in a git repo
   at all" already falls back to `--project` with a warning
   (`main.go:499-507`). "In a git repo but `git diff ... HEAD` fails"
   (detached HEAD, no commits yet) does not — it surfaces as one opaque,
   untyped issue (`main.go:751-764`) with no language/file/code and no
   fallback. Fix: same fallback treatment as the no-git-repo case, or at
   minimum a distinguishable error rather than a bare string.

## P0 — close the repo-content RCE

4. **`npm run <script>` executes whatever the target repo's `package.json`
   declares, unconditionally, no allowlist** (`main.go:913-943`). Given
   taste's actual use case — pointed at a repo an agent just cloned or was
   told to "clean up" — this is arbitrary code execution from untrusted
   content, on by default, undocumented as a risk anywhere. Fix: gate
   behind explicit confirmation (`--allow-scripts` / `TASTE_ALLOW_SCRIPTS`);
   see `FLAVORS_PROPOSAL.md`'s `requires_confirmation` design, which folds
   this fix into the same config work as P2 rather than needing a separate
   patch.

Lower-severity security notes worth a follow-up pass, not blocking release:
env-var tool overrides are trusted unconditionally with no signal an
override is active beyond a JSON field (`main.go:415-461`); repo-local
`node_modules/.bin` binaries are preferred over system tools with no
warning that a repo is supplying its own tool binary (`main.go:471-484`);
no path-containment check on `targets`/`--stdin-json` against the working
tree (`main.go:680-736`).

## P1 — stand up CI, stop trusting the maintainer's machine

5. **No CI configuration exists at all.** Every test run to date has only
   ever happened on one machine with whatever tool versions/OS happen to be
   installed there. This is the root cause of "works on my machine" risk
   for a public release.

6. **Integration tests skip silently when tools are missing**
   (`integration_test.go:12-14, 39-41, 102-104`) — a machine (or CI run)
   missing gopls/tsserver/npm gets an all-green `go test` while covering
   almost none of the real LSP paths.

7. **Version-sensitive substring assertions with no pinned tool version**
   (matching literal strings like `"cannot use 1"`, error code `2322`
   against real gopls/tsc output) — a future tool release reformatting
   diagnostics breaks these tests silently and machine-dependently.

8. **Install hints assume Homebrew** (`main.go:373, 377-378, 989`) — a
   Linux user without Homebrew gets a `brew install shellcheck` hint that
   doesn't apply to them. Windows is explicitly out of scope; this is
   about macOS vs. Linux install hints, not a Windows port.

9. No end-to-end subprocess smoke test (everything calls `run()` in-process
   — nothing exercises the actual compiled binary, its exit codes as seen
   externally, or `--json` vs human output at the process boundary).
   `version` (`main.go:17`) is hand-edited, not ldflags-injected from git
   tags, so it can drift from actual releases unnoticed.

Target: a GitHub Actions matrix (macOS + Linux at minimum) that installs
the real toolchain (Go, Node, gopls, typescript-language-server, shellcheck,
shfmt, bash-language-server) and runs the integration suite for real,
failing the build if a test skips instead of running.

## P1 — reconcile the taste/pi-taste schema

10. **`checks` vs `commands` are two different Go structs crammed into one
    TypeScript type in `pi-taste`.** `checkItem` (`Name/Language/Available/
    Path/Env/Install`, `main.go:51-58`) and `commandItem` (`Name/Status/
    Summary`, `main.go:45-49`) are distinct shapes; pi-taste's `Command`
    type unions fields from both, including `ran`/`status` on `available`,
    which neither real struct ever emits together. `SURFACE_CONTRACT.md`
    documents a shape taste can't actually produce.

11. **`column` is documented on both sides, never populated.** Both taste's
    (implicit) contract and pi-taste's `Issue` type declare it; `lsp.go:238`
    already parses `diag.Range.Start.Character` and then drops it before
    building the `issueItem`.

12. **`schema_version` is decorative** — hardcoded `1` in three places
    (`main.go:61, 76`; `index.ts:67`) with no bump discipline. This is the
    PRD's own open question #1, still unresolved.

13. **pi-taste re-implements taste's issue sort and re-caps issues taste
    already capped** (`index.ts:342-354` duplicating `main.go:579-600`;
    `index.ts:224-226` re-slicing after `main.go:543-545` already
    truncated) — the exact "must not reimplement quality logic" violation
    the boundary forbids, plus two independently-tunable max-issue
    constants (40 vs 200, clamped 1-500 vs 1-10000) that can silently
    diverge.

Target: one unified check/command shape, `column` populated end-to-end,
a real `schema_version` bump policy, and pi-taste's wrapper trusting
taste's sort/cap instead of redoing it. Do this **before or alongside** P2
(the flavor registry), since the registry's dispatcher will be generating
`commands`/`checks` output and shouldn't be built against a shape that's
about to change underneath it.

## P2 — config-driven flavor registry

See `FLAVORS_PROPOSAL.md` for the full design: TOML-based flavor entries
(tools + fix/taste/strict action steps), explicit `per_file` contract for
custom tools, `fixable` flag replacing pi-taste's string-matching heuristic
(closes #16 below), `requires_root_marker`/`requires_script` as a
generalized strict-readiness gate (closes #17), and `requires_confirmation`
gating for repo-declared script execution (closes P0 item #4 above).

Also closes:

14. **`--strict` readiness is ad hoc per language** — Go gates on `go.mod`
    existing, JS gates on a package.json script existing, Bash has no gate
    at all and always runs shellcheck. Three different implicit
    preconditions, hand-written.

15. **`allToolDefs()` is data-shaped but unused by dispatch** —
    `runGo`/`runJS`/`runBash` hardcode literal tool names
    (`main.go:838, 918, 986, 995`) instead of iterating it; today it only
    feeds `--flavors` output, not execution.

## P3 — polish

- Linux-aware install hints (drop the `brew install` assumption when not
  on macOS). Windows is explicitly out of scope.
- `version` wired to ldflags from git tags instead of a hand-edited const.
- A subprocess-level e2e smoke test covering `--json`/human output and
  exit codes 0/1/2 as seen externally, not just via the in-process `run()`
  call the current tests use.

## P4 — taste-mcp: a standalone MCP server

`pi-taste` only reaches Pi agents (`pi.registerTool`, the
`@earendil-works/pi-coding-agent` extension API). Most of the value isn't
Pi-specific: `buildTasteArgs`, `runTaste`, `normalizeTasteResult`, and the
schema/next-action text in `src/index.ts` are just "shell out to the
`taste` binary and shape the result" — nothing in that logic depends on
Pi. Standing up a standard MCP server (stdio transport, `@modelcontextprotocol/sdk`)
would let any MCP-compatible client (Claude Desktop, Claude Code, Cursor,
etc.) use taste, not just Pi agents.

Full design, package layout, and rollout order:
`pi-taste`'s `TASTE_MCP_PROPOSAL.md`.

16. **Extract the transport-agnostic core out of `pi-taste`** into an npm
    workspace package (`@taste/core`, unpublished): types, arg-building,
    result normalization, an `Executor` interface each wrapper implements
    for its own subprocess mechanism. Both `pi-taste` and `taste-mcp`
    become thin adapters over it, so a schema/behavior change (like the
    `--fix`→`--autofix` rename) only has one place to land. Also the first
    real unit tests for this logic — `pi-taste` currently has none.

17. **New `taste-mcp` package** in the same workspace, registering the
    same tool surface (targets/changed/project/autofix/strict/timeout_ms/
    max_issues) via the MCP SDK (Zod schemas, stdio transport) instead of
    Pi's extension API/typebox, built to `dist/` and runnable via
    `npx taste-mcp`.

## Sequencing rationale

P0 items ship before anything else touches this code, since they're either
actively lying about pass/fail (undermining the product's reason to exist)
or an active RCE risk once this goes public. P1's CI work should land
early too, if only so every subsequent change (schema reconciliation, the
registry) is verified on a clean machine instead of trusting local state —
the same failure mode the critique's testing/packaging findings describe.
Schema reconciliation before or alongside P2 avoids building the new
data-driven dispatcher against a `commands`/`checks` shape that's about to
be unified. P3 has no urgency; it's cleanup once the above lands. P4
(`taste-mcp`) is a new distribution surface, not a fix -- it can start
whenever, but extracting the shared core (item 16) is worth doing before
`pi-taste` accumulates more Pi-specific logic tangled into it, since that
tangling is exactly what makes the extraction harder later.
