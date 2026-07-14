package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// var, not const: overwritten at build time via -ldflags "-X main.version=...".
// This fallback only shows up in a go run/go build with no ldflags.
var version = "0.1.0-beta.2"

// schema_version bumps when result, issueItem, checkItem, commandItem, or
// flavorsResult JSON shape changes break old consumers, OR when a new value
// is added to an existing enum-like field (e.g. status) that an old
// consumer's exhaustive branching wouldn't recognize. Additive optional
// fields do not need a bump. v2: added status="incomplete" and the
// Incomplete field.
const schemaVersion = 2

const defaultMaxIssues = 200

type fixedItem struct {
	Language string `json:"language"`
	Kind     string `json:"kind"`
	Files    int    `json:"files"`
}

type issueItem struct {
	Language string `json:"language"`
	Severity string `json:"severity"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type warningItem struct {
	Tool        string `json:"tool,omitempty"`
	Language    string `json:"language,omitempty"`
	Message     string `json:"message"`
	Install     string `json:"install,omitempty"`
	EnvOverride string `json:"env_override,omitempty"`
}

type commandItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type checkItem struct {
	Name      string `json:"name"`
	Language  string `json:"language"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Env       string `json:"env,omitempty"`
	Install   string `json:"install,omitempty"`
}

// incompleteItem records a required diagnostic tool that failed to run
// (missing, crashed, or timed out) for files actually in scope. This is
// distinct from issueItem: an issue means the tool ran and found a real
// problem; an incompleteItem means readiness could not be established at
// all. Keeping them separate lets status distinguish "confirmed broken"
// (fail) from "couldn't verify" (incomplete) instead of collapsing both
// into the same signal.
type incompleteItem struct {
	Language string `json:"language"`
	Tool     string `json:"tool"`
	Reason   string `json:"reason"`
}

type result struct {
	SchemaVersion int              `json:"schema_version"`
	Status        string           `json:"status"`
	Scope         string           `json:"scope"`
	Summary       string           `json:"summary"`
	Level         string           `json:"level"`
	Header        string           `json:"header"`
	Checks        []checkItem      `json:"checks"`
	Fixed         []fixedItem      `json:"fixed"`
	Issues        []issueItem      `json:"issues"`
	TotalIssues   int              `json:"total_issues"`
	Warnings      []warningItem    `json:"warnings"`
	Commands      []commandItem    `json:"commands"`
	Incomplete    []incompleteItem `json:"incomplete"`
}

type flavorsResult struct {
	SchemaVersion int         `json:"schema_version"`
	Status        string      `json:"status"`
	Summary       string      `json:"summary"`
	Checks        []checkItem `json:"checks"`
}

type options struct {
	Intent       string
	Scope        string
	Paths        []string
	JSONOut      bool
	Level        string
	ShowFlavors  bool
	AllowScripts bool
	MaxIssues    int
}

type stdinPayload struct {
	Scope string   `json:"scope"`
	Paths []string `json:"paths"`
}

type toolDef struct {
	Name     string
	Language string
	Env      string
	Install  string
	LocalNPM bool
}

type cliError string

func (e cliError) Error() string { return string(e) }

// gitStateError signals a git invocation failed not due to a missing tool or
// filesystem problem, but due to an edge case of repo state (e.g. detached
// HEAD with no commits yet). Callers can treat it as "not actionable" and
// fall back rather than surfacing an opaque error.
type gitStateError struct{ inner error }

func (e *gitStateError) Error() string {
	if e.inner == nil {
		return "git state error"
	}
	return e.inner.Error()
}

func (e *gitStateError) Unwrap() error { return e.inner }

func isGitStateError(err error) bool {
	var gse *gitStateError
	return errors.As(err, &gse)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: taste [targets...] [--autofix|--dry] [--easy|--strict] [--json] [--allow-scripts]

Targets:
  files or directories; multiple allowed
  no targets defaults to changed files in git, else project
  use -- before targets that begin with -

Flags:
  --autofix             safe autofix only; does not diagnose (run again without --autofix to check)
  --dry                 diagnostics only; default
  --easy                fast/local checks; default
  --strict              complete readiness checks
  --changed             changed files from git: staged+unstaged vs HEAD, plus untracked supported files
  --project             whole project
  --flavors             list available diagnostic/check flavors
  --allow-scripts       allow npm run <script> execution from repo package.json
  --max-issues <n>      cap JSON issues; default 200

Examples:
  taste main.go
  taste main.go scripts/dev.sh --autofix
  taste . --strict
  taste --changed --strict --json`)
}

func usageTo(w io.Writer) {
	fmt.Fprintln(w, `usage: taste [targets...] [--autofix|--dry] [--easy|--strict] [--json] [--allow-scripts]

Targets:
  files or directories; multiple allowed
  no targets defaults to changed files in git, else project
  use -- before targets that begin with -

Flags:
  --autofix             safe autofix only; does not diagnose (run again without --autofix to check)
  --dry                 diagnostics only; default
  --easy                fast/local checks; default
  --strict              complete readiness checks
  --changed             changed files from git: staged+unstaged vs HEAD, plus untracked supported files
  --project             whole project
  --flavors             list available diagnostic/check flavors
  --allow-scripts       allow npm run <script> execution from repo package.json
  --max-issues <n>      cap JSON issues; default 200

Examples:
  taste main.go
  taste main.go scripts/dev.sh --autofix
  taste . --strict
  taste --changed --strict --json`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithFlavors(args, stdin, stdout, stderr, nil)
}

// runWithFlavors is run with the flavor registry injected instead of resolved
// from getFlavors(). Production always passes nil (via run), which falls
// back to the real embedded/project/user registry. Tests exercising generic
// dispatch mechanics (autofix/diagnose decoupling, incomplete-tool handling,
// script gating, unfixable-tool warnings) should pass a synthetic flavor
// here instead of relying on the real go/js/bash config -- that keeps the
// engine's tests from breaking when the shipped tool config changes for
// reasons unrelated to the mechanism under test.
func runWithFlavors(args []string, stdin io.Reader, stdout, stderr io.Writer, flavors []flavorDef) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			fmt.Fprintf(stdout, "taste %s\n", version)
			return 0
		case "help", "--help", "-h":
			usageTo(stdout)
			return 0
		}
	}

	opts, err := parseArgs(args, stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		usageTo(stderr)
		return 2
	}
	if opts.ShowFlavors {
		if err := printAvailability(stdout, runFlavors(), opts.JSONOut); err != nil {
			fmt.Fprintln(stderr, err)
			return 10
		}
		return 0
	}

	var res result
	if flavors != nil {
		res = runTasteWithFlavors(opts, flavors, "")
	} else {
		res = runTaste(opts)
	}
	if opts.JSONOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(stderr, err)
			return 10
		}
	} else {
		printHuman(stdout, res)
	}
	switch res.Status {
	case "fail":
		return 1
	case "incomplete":
		return 3
	default:
		return 0
	}
}

func parseArgs(args []string, stdin io.Reader) (options, error) {
	opts := options{Intent: "check", Level: "easy", MaxIssues: defaultMaxIssues}
	if truthyEnv(os.Getenv("TASTE_ALLOW_SCRIPTS")) {
		opts.AllowScripts = true
	}
	afterSeparator := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if afterSeparator {
			if opts.Scope == "changed" || opts.Scope == "project" || opts.Scope == "stdin-json" {
				return options{}, cliError("targets cannot be combined with " + opts.Scope + " scope")
			}
			opts.Scope = "paths"
			opts.Paths = append(opts.Paths, arg)
			continue
		}
		switch {
		case arg == "--":
			afterSeparator = true
		case arg == "--json":
			opts.JSONOut = true
		case arg == "--easy":
			opts.Level = "easy"
		case arg == "--strict":
			opts.Level = "strict"
		case arg == "--autofix":
			opts.Intent = "autofix"
		case arg == "--dry":
			opts.Intent = "check"
		case arg == "--allow-scripts":
			opts.AllowScripts = true
		case strings.HasPrefix(arg, "--allow-scripts="):
			opts.AllowScripts = truthyArg(strings.TrimPrefix(arg, "--allow-scripts="))
		case arg == "--changed":
			if len(opts.Paths) > 0 {
				return options{}, cliError("--changed cannot be combined with targets")
			}
			if opts.Scope == "project" || opts.Scope == "stdin-json" {
				return options{}, cliError("--changed cannot be combined with " + opts.Scope)
			}
			opts.Scope = "changed"
		case arg == "--project":
			if len(opts.Paths) > 0 {
				return options{}, cliError("--project cannot be combined with targets")
			}
			if opts.Scope == "changed" || opts.Scope == "stdin-json" {
				return options{}, cliError("--project cannot be combined with " + opts.Scope)
			}
			opts.Scope = "project"
		case arg == "--stdin-json":
			if len(opts.Paths) > 0 {
				return options{}, cliError("--stdin-json cannot be combined with targets")
			}
			if opts.Scope == "changed" || opts.Scope == "project" {
				return options{}, cliError("--stdin-json cannot be combined with " + opts.Scope)
			}
			opts.Scope = "stdin-json"
			payload, err := readStdinPayload(stdin)
			if err != nil {
				return options{}, err
			}
			if payload.Scope != "" {
				opts.Scope = payload.Scope
			}
			opts.Paths = append(opts.Paths, payload.Paths...)
		case arg == "--flavors":
			opts.ShowFlavors = true
		case arg == "--max-issues":
			if i+1 >= len(args) {
				return options{}, cliError("--max-issues needs a value")
			}
			i++
			value, err := parseMaxIssues(args[i])
			if err != nil {
				return options{}, err
			}
			opts.MaxIssues = value
		case strings.HasPrefix(arg, "--max-issues="):
			value, err := parseMaxIssues(strings.TrimPrefix(arg, "--max-issues="))
			if err != nil {
				return options{}, err
			}
			opts.MaxIssues = value
		case strings.HasPrefix(arg, "--"):
			return options{}, cliError("unknown flag: " + arg)
		default:
			if opts.Scope == "changed" || opts.Scope == "project" || opts.Scope == "stdin-json" {
				return options{}, cliError("targets cannot be combined with " + opts.Scope + " scope")
			}
			opts.Scope = "paths"
			opts.Paths = append(opts.Paths, arg)
		}
	}
	if opts.Level != "easy" && opts.Level != "strict" {
		return options{}, cliError("unknown level: " + opts.Level)
	}
	if opts.Intent == "autofix" && opts.Scope == "project" {
		return options{}, cliError("--project cannot be combined with --autofix in v0; pass explicit targets or use --changed --autofix")
	}
	if opts.Intent == "autofix" && opts.Level == "strict" {
		return options{}, cliError("--strict cannot be combined with --autofix; --autofix mutates only and does not diagnose -- run taste again without --autofix to diagnose, optionally with --strict")
	}
	return opts, nil
}

func parseMaxIssues(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 10000 {
		return 0, cliError("--max-issues must be an integer between 1 and 10000")
	}
	return value, nil
}

func truthyEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func truthyArg(raw string) bool { return truthyEnv(raw) }

func readStdinPayload(r io.Reader) (stdinPayload, error) {
	var payload stdinPayload
	data, err := io.ReadAll(r)
	if err != nil {
		return payload, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return payload, cliError("--stdin-json needs JSON input")
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func printAvailability(w io.Writer, res flavorsResult, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	printFlavorsHuman(w, res)
	return nil
}

func runFlavors() flavorsResult {
	checks := checksForFlavorNames(nil)
	available := 0
	for _, check := range checks {
		if check.Available {
			available++
		}
	}
	return flavorsResult{SchemaVersion: schemaVersion, Status: "ok", Summary: fmt.Sprintf("%d/%d checks available", available, len(checks)), Checks: checks}
}

func printFlavorsHuman(w io.Writer, res flavorsResult) {
	fmt.Fprintln(w, res.Summary)
	for _, check := range res.Checks {
		status := "missing"
		path := check.Install
		if check.Available {
			status = "ok"
			path = check.Path
		}
		fmt.Fprintf(w, "- %s %s %s [%s] env:%s\n", status, check.Name, check.Language, path, check.Env)
	}
}

func resolveTool(def toolDef) (string, bool) {
	if override := os.Getenv(def.Env); override != "" {
		if filepath.IsAbs(override) || strings.ContainsRune(override, os.PathSeparator) {
			if fileExists(override) {
				return override, true
			}
			return override, false
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, true
		}
		return override, false
	}
	if def.LocalNPM {
		if p, ok := findLocalNPMBin(def.Name); ok {
			return p, true
		}
	}
	if p, err := exec.LookPath(def.Name); err == nil {
		return p, true
	}
	return "", false
}

func resolveToolInDir(def toolDef, dir string) (string, bool) {
	if override := os.Getenv(def.Env); override != "" {
		if filepath.IsAbs(override) || strings.ContainsRune(override, os.PathSeparator) {
			if fileExists(override) {
				return override, true
			}
			return override, false
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, true
		}
		return override, false
	}
	if def.LocalNPM {
		if p, ok := findLocalNPMBinFrom(dir, def.Name); ok {
			return p, true
		}
	}
	if p, err := exec.LookPath(def.Name); err == nil {
		return p, true
	}
	return "", false
}

func findLocalNPMBin(name string) (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	return findLocalNPMBinFrom(dir, name)
}

func findLocalNPMBinFrom(dir, name string) (string, bool) {
	for {
		candidate := filepath.Join(dir, "node_modules", ".bin", name)
		if fileExists(candidate) {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func runTaste(opts options) result {
	flavors, flavorWarning := getFlavors()
	return runTasteWithFlavors(opts, flavors, flavorWarning)
}

// runTasteWithFlavors is runTaste with the flavor registry taken as an
// explicit input instead of resolved internally via getFlavors(). This is
// the seam that lets engine-mechanism tests (autofix/diagnose decoupling,
// incomplete-tool handling, script gating, unfixable-tool warnings) run
// against a synthetic flavor rather than depending on the real go/js/bash
// defaults, so those tests don't break for reasons unrelated to the
// mechanism they're actually checking.
func runTasteWithFlavors(opts options, flavors []flavorDef, flavorWarning string) result {
	scopeWasImplicit := opts.Scope == ""
	res := result{SchemaVersion: schemaVersion, Scope: opts.Scope, Level: opts.Level, Checks: []checkItem{}, Fixed: []fixedItem{}, Issues: []issueItem{}, Warnings: []warningItem{}, Commands: []commandItem{}}
	if res.Level == "" {
		res.Level = "easy"
	}
	if res.Scope == "" {
		if inGitRepo() {
			res.Scope = "changed"
			res.Warnings = append(res.Warnings, warningItem{Message: "defaulted to --changed"})
		} else {
			res.Scope = "project"
			res.Warnings = append(res.Warnings, warningItem{Message: "not in git repo; defaulted to --project"})
		}
	}

	paths, err := collectFiles(res.Scope, opts.Paths)
	if err != nil {
		// A changed-files run can fail purely due to repo state (e.g. detached
		// HEAD with no commits yet). Only fall back to --project when the
		// scope was implicit (mirroring the not-in-git-repo path); an
		// explicit --changed that can't resolve should surface as a clear
		// error rather than silently widening scope the user deliberately
		// narrowed.
		if scopeWasImplicit && res.Scope == "changed" && isGitStateError(err) {
			res.Warnings = append(res.Warnings, warningItem{Message: "git changed-file detection failed (" + err.Error() + "); defaulted to --project"})
			res.Scope = "project"
			paths, err = collectFiles(res.Scope, opts.Paths)
		}
	}
	if err != nil {
		res.Issues = append(res.Issues, issueItem{Severity: "error", Message: err.Error()})
		return finalize(res, opts.MaxIssues, false)
	}
	if flavorWarning != "" {
		res.Warnings = append(res.Warnings, warningItem{Message: "flavor config: " + flavorWarning})
	}
	groups := classifyByFlavor(paths, flavors)
	matched := map[string]bool{}
	for name, files := range groups {
		if len(files) > 0 {
			matched[name] = true
		}
	}
	res.Checks = checksForFlavorNames(matched)

	format := opts.Intent == "autofix"
	// --autofix mutates only; it must not silently also run the full diagnose
	// pass (spawning LSP tools, waiting on round-trips) just because a
	// caller asked for a fix. Diagnosing after fixing is two explicit
	// calls, not one implicit one.
	diag := opts.Intent == "check"

	anyMatched := false
	for _, fl := range flavors {
		files := groups[fl.Name]
		if len(files) == 0 {
			continue
		}
		anyMatched = true
		if format {
			runFlavorAction(&res, fl, files, "fix", opts.AllowScripts)
			if unfixable := fl.unfixableDiagnosticTools(); len(unfixable) > 0 {
				res.Warnings = append(res.Warnings, warningItem{Message: fmt.Sprintf("%s: %s can report issues --autofix cannot resolve (no fix step configured); run without --autofix to check", fl.Name, strings.Join(unfixable, ", "))})
			}
		}
		if diag {
			runFlavorAction(&res, fl, files, "taste", opts.AllowScripts)
			if res.Level == "strict" {
				runFlavorAction(&res, fl, files, "strict", opts.AllowScripts)
			}
		}
	}
	if len(paths) == 0 || !anyMatched {
		res.Warnings = append(res.Warnings, warningItem{Message: "no supported source files matched scope"})
	}
	return finalize(res, opts.MaxIssues, diag)
}

func finalize(res result, maxIssues int, diagnosed bool) result {
	res = ensureResultSlices(res)
	res.Header = checksHeader(res)
	res.Issues = ensureIssues(sortIssues(res.Issues))
	res.TotalIssues = len(res.Issues)
	if maxIssues <= 0 {
		maxIssues = defaultMaxIssues
	}
	if len(res.Issues) > maxIssues {
		res.Issues = res.Issues[:maxIssues]
	}
	if res.TotalIssues > 0 {
		res.Status = "fail"
		res.Summary = fmt.Sprintf("FAIL %d issues, %d warnings", res.TotalIssues, len(res.Warnings))
		return res
	}
	if len(res.Incomplete) > 0 {
		res.Status = "incomplete"
		res.Summary = fmt.Sprintf("INCOMPLETE %d tool(s) unavailable, %d warnings", len(res.Incomplete), len(res.Warnings))
		return res
	}
	res.Status = "pass"
	if diagnosed {
		res.Summary = fmt.Sprintf("PASS fixed: %s; remaining: 0", fixedSummary(res.Fixed))
	} else {
		res.Summary = fmt.Sprintf("PASS fixed: %s; not diagnosed -- run again without --autofix to check for remaining issues", fixedSummary(res.Fixed))
	}
	return res
}
func ensureResultSlices(res result) result {
	if res.Checks == nil {
		res.Checks = []checkItem{}
	}
	if res.Fixed == nil {
		res.Fixed = []fixedItem{}
	}
	res.Issues = ensureIssues(res.Issues)
	if res.Warnings == nil {
		res.Warnings = []warningItem{}
	}
	if res.Commands == nil {
		res.Commands = []commandItem{}
	}
	if res.Incomplete == nil {
		res.Incomplete = []incompleteItem{}
	}
	return res
}

func ensureIssues(issues []issueItem) []issueItem {
	if issues == nil {
		return []issueItem{}
	}
	return issues
}

func sortIssues(issues []issueItem) []issueItem {
	out := append([]issueItem(nil), issues...)
	sort.SliceStable(out, func(i, j int) bool {
		if severityRank(out[i].Severity) != severityRank(out[j].Severity) {
			return severityRank(out[i].Severity) < severityRank(out[j].Severity)
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if lineRank(out[i].Line) != lineRank(out[j].Line) {
			return lineRank(out[i].Line) < lineRank(out[j].Line)
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func severityRank(severity string) int {
	if severity == "error" {
		return 0
	}
	if severity == "warning" {
		return 1
	}
	return 2
}

func lineRank(line int) int {
	if line == 0 {
		return int(^uint(0) >> 1)
	}
	return line
}

func checksHeader(res result) string {
	if len(res.Commands) > 0 {
		parts := make([]string, 0, len(res.Commands))
		for _, cmd := range res.Commands {
			parts = append(parts, cmd.Name+":"+cmd.Status)
		}
		return "checks: " + strings.Join(parts, ", ")
	}
	if len(res.Checks) > 0 {
		parts := make([]string, 0, len(res.Checks))
		for _, check := range res.Checks {
			status := "missing"
			if check.Available {
				status = "available"
			}
			parts = append(parts, check.Name+":"+status)
		}
		return "checks: " + strings.Join(parts, ", ")
	}
	return "checks: none"
}

func fixedSummary(fixed []fixedItem) string {
	if len(fixed) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(fixed))
	for _, f := range fixed {
		parts = append(parts, fmt.Sprintf("%s %s %d files", f.Language, f.Kind, f.Files))
	}
	return strings.Join(parts, "; ")
}

func printHuman(w io.Writer, res result) {
	fmt.Fprintln(w, res.Summary)
	fmt.Fprintln(w, res.Header)
	limit := 20
	for i, issue := range res.Issues {
		if i >= limit {
			fmt.Fprintf(w, "- ... %d more issues\n", len(res.Issues)-limit)
			break
		}
		loc := issue.File
		if issue.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, issue.Line)
		}
		if loc != "" {
			fmt.Fprintf(w, "- %s %s %s\n", loc, issue.Code, issue.Message)
		} else {
			fmt.Fprintf(w, "- %s %s\n", issue.Code, issue.Message)
		}
	}
	for i, warning := range res.Warnings {
		if i >= 5 {
			fmt.Fprintf(w, "- ... %d more warnings\n", len(res.Warnings)-5)
			break
		}
		fmt.Fprintf(w, "warn: %s\n", warning.Message)
	}
}

func collectFiles(scope string, paths []string) ([]string, error) {
	switch scope {
	case "paths", "stdin-json":
		return targetFiles(paths)
	case "changed":
		return gitChangedFiles()
	case "project":
		return projectFiles(".", true)
	default:
		if len(paths) > 0 {
			return targetFiles(paths)
		}
		return nil, cliError("unknown scope: " + scope)
	}
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, p := range paths {
		p = filepath.Clean(p)
		if p == "." || p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func targetFiles(targets []string) ([]string, error) {
	var files []string
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		clean := filepath.Clean(target)
		info, err := os.Stat(clean)
		if err != nil {
			files = append(files, clean)
			continue
		}
		if info.IsDir() {
			found, err := projectFiles(clean, false)
			if err != nil {
				return nil, err
			}
			files = append(files, found...)
			continue
		}
		files = append(files, clean)
	}
	return cleanPaths(files), nil
}

func gitChangedFiles() ([]string, error) {
	tracked, err := gitChangedTrackedFiles()
	if err != nil {
		return nil, err
	}
	untracked, err := gitUntrackedFiles()
	if err != nil {
		return nil, err
	}
	return cleanPaths(append(tracked, untracked...)), nil
}

func gitChangedTrackedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", "HEAD", "--")
	out, err := cmd.Output()
	if err != nil {
		wrapped := fmt.Errorf("git changed-file detection failed: %w", err)
		if isEmptyRepoRevisionError(err) {
			return nil, &gitStateError{inner: wrapped}
		}
		return nil, wrapped
	}
	return splitGitFileList(out), nil
}

// isEmptyRepoRevisionError reports whether err is git failing to resolve
// HEAD specifically because the repo has no commits yet (a real, harmless
// repo-state edge case) rather than some other failure (permissions, disk
// I/O, a corrupted repo) that happens to also be non-zero-exit.
func isEmptyRepoRevisionError(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	stderr := strings.ToLower(string(exitErr.Stderr))
	return strings.Contains(stderr, "bad revision") && strings.Contains(stderr, "head")
}

func gitUntrackedFiles() ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git untracked-file detection failed: %w", err)
	}
	files := splitGitFileList(out)
	filtered := files[:0]
	for _, file := range files {
		if isKnownSource(file) {
			filtered = append(filtered, file)
		}
	}
	return filtered, nil
}

func splitGitFileList(out []byte) []string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

func projectFiles(root string, skipTestdata bool) ([]string, error) {
	var paths []string
	skipDirs := map[string]bool{".git": true, "node_modules": true, "dist": true, "build": true, "coverage": true, "vendor": true}
	if skipTestdata {
		skipDirs["testdata"] = true
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() && skipDirs[name] {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		path = strings.TrimPrefix(filepath.Clean(path), "."+string(os.PathSeparator))
		if isKnownSource(path) {
			paths = append(paths, path)
		}
		return nil
	})
	return cleanPaths(paths), err
}

func isKnownSource(p string) bool {
	flavors, _ := getFlavors()
	for _, fl := range flavors {
		if fl.matches(p) {
			return true
		}
	}
	return false
}

// reportToolFailure records a required diagnostic tool that failed to run
// (missing, crashed, or timed out) for files in scope. It marks the run
// incomplete rather than appending an issue, so a missing tool is
// distinguished from a confirmed real problem in the code.
func reportToolFailure(res *result, language, tool string, err error) {
	res.Warnings = append(res.Warnings, warningItem{Language: language, Message: err.Error()})
	res.Incomplete = append(res.Incomplete, incompleteItem{Language: language, Tool: tool, Reason: err.Error()})
}

func gateRepoScript(res *result, language, name string, allowScripts bool) bool {
	if allowScripts {
		return true
	}
	res.Commands = append(res.Commands, commandItem{Name: name, Status: "skip"})
	res.Warnings = append(res.Warnings, warningItem{Language: language, Message: name + " withheld: executes repo-declared code; pass --allow-scripts or set TASTE_ALLOW_SCRIPTS=1"})
	return false
}

func packageScripts(dir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var payload struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil
	}
	out := map[string]bool{}
	for k := range payload.Scripts {
		out[k] = true
	}
	return out
}

func runExternal(name string, args ...string) (string, string) {
	return runExternalInDir(".", name, args...)
}

func runExternalInDir(dir, name string, args ...string) (string, string) {
	status, raw := runExternalInDirRaw(dir, name, args...)
	return status, summarizeOutput([]byte(raw))
}

// runExternalInDirRaw is runExternalInDir without summarizeOutput's 3-line
// cap, for callers that need to inspect the tool's full output themselves
// (e.g. counting how many files a fixer actually reported changing).
func runExternalInDirRaw(dir, name string, args ...string) (string, string) {
	path, ok := resolveToolInDir(toolDefByName(name), dir)
	if !ok {
		return "fail", fmt.Sprintf("%s not found; override with %s", name, toolDefByName(name).Env)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "fail", text
	}
	return "pass", text
}

func summarizeOutput(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
		if len(clean) >= 3 {
			break
		}
	}
	return strings.Join(clean, " | ")
}

func inGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
