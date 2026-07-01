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

const version = "0.1.0-beta.1"

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

type result struct {
	SchemaVersion int           `json:"schema_version"`
	Status        string        `json:"status"`
	Scope         string        `json:"scope"`
	Summary       string        `json:"summary"`
	Level         string        `json:"level"`
	Header        string        `json:"header"`
	Checks        []checkItem   `json:"checks"`
	Fixed         []fixedItem   `json:"fixed"`
	Issues        []issueItem   `json:"issues"`
	TotalIssues   int           `json:"total_issues"`
	Warnings      []warningItem `json:"warnings"`
	Commands      []commandItem `json:"commands"`
}

type flavorsResult struct {
	SchemaVersion int         `json:"schema_version"`
	Status        string      `json:"status"`
	Summary       string      `json:"summary"`
	Checks        []checkItem `json:"checks"`
}

type options struct {
	Intent      string
	Scope       string
	Paths       []string
	JSONOut     bool
	Level       string
	ShowFlavors bool
	MaxIssues   int
}

type stdinPayload struct {
	Scope string   `json:"scope"`
	Paths []string `json:"paths"`
}

type fileGroups struct {
	Go   []string
	JS   []string
	Bash []string
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

func usage() {
	fmt.Fprintln(os.Stderr, `usage: taste [targets...] [--fix|--dry] [--easy|--strict] [--json]

Targets:
  files or directories; multiple allowed
  no targets defaults to changed files in git, else project
  use -- before targets that begin with -

Flags:
  --fix                 safe autofix, then diagnostics
  --dry                 diagnostics only; default
  --easy                fast/local checks; default
  --strict              complete readiness checks
  --changed             changed files from git: staged+unstaged vs HEAD, plus untracked supported files
  --project             whole project
  --flavors             list available diagnostic/check flavors
  --max-issues <n>      cap JSON issues; default 200

Examples:
  taste main.go
  taste main.go scripts/dev.sh --fix
  taste . --strict
  taste --changed --strict --json`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
			return 1
		}
		return 0
	}

	res := runTaste(opts)
	if opts.JSONOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		printHuman(stdout, res)
	}
	if res.Status == "fail" {
		return 1
	}
	return 0
}

func usageTo(w io.Writer) {
	fmt.Fprintln(w, `usage: taste [targets...] [--fix|--dry] [--easy|--strict] [--json]

Targets:
  files or directories; multiple allowed
  no targets defaults to changed files in git, else project
  use -- before targets that begin with -

Flags:
  --fix                 safe autofix, then diagnostics
  --dry                 diagnostics only; default
  --easy                fast/local checks; default
  --strict              complete readiness checks
  --changed             changed files from git: staged+unstaged vs HEAD, plus untracked supported files
  --project             whole project
  --flavors             list available diagnostic/check flavors
  --max-issues <n>      cap JSON issues; default 200

Examples:
  taste main.go
  taste main.go scripts/dev.sh --fix
  taste . --strict
  taste --changed --strict --json`)
}

func parseArgs(args []string, stdin io.Reader) (options, error) {
	opts := options{Intent: "check", Level: "easy", MaxIssues: defaultMaxIssues}
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
		case arg == "--fix":
			opts.Intent = "fix"
		case arg == "--dry":
			opts.Intent = "check"
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
	if opts.Intent == "fix" && opts.Scope == "project" {
		return options{}, cliError("--project cannot be combined with --fix in v0; pass explicit targets or use --changed --fix")
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
	checks := availableChecks(allToolDefs())
	available := 0
	for _, check := range checks {
		if check.Available {
			available++
		}
	}
	return flavorsResult{SchemaVersion: 1, Status: "ok", Summary: fmt.Sprintf("%d/%d checks available", available, len(checks)), Checks: checks}
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

func allToolDefs() []toolDef {
	return []toolDef{
		{Name: "gofmt", Language: "go", Env: "TASTE_GOFMT", Install: "ships with Go"},
		{Name: "go", Language: "go", Env: "TASTE_GO", Install: "install Go from https://go.dev/dl/"},
		{Name: "gopls", Language: "go", Env: "TASTE_GOPLS", Install: "go install golang.org/x/tools/gopls@latest"},
		{Name: "npm", Language: "javascript", Env: "TASTE_NPM", Install: "install Node.js/npm"},
		{Name: "prettier", Language: "javascript", Env: "TASTE_PRETTIER", Install: "npm install -D prettier", LocalNPM: true},
		{Name: "eslint", Language: "javascript", Env: "TASTE_ESLINT", Install: "npm install -D eslint", LocalNPM: true},
		{Name: "typescript-language-server", Language: "javascript", Env: "TASTE_TYPESCRIPT_LANGUAGE_SERVER", Install: "npm install -D typescript-language-server typescript", LocalNPM: true},
		{Name: "bash", Language: "bash", Env: "TASTE_BASH", Install: "install bash"},
		{Name: "shellcheck", Language: "bash", Env: "TASTE_SHELLCHECK", Install: "brew install shellcheck"},
		{Name: "shfmt", Language: "bash", Env: "TASTE_SHFMT", Install: "brew install shfmt"},
		{Name: "bash-language-server", Language: "bash", Env: "TASTE_BASH_LANGUAGE_SERVER", Install: "npm install -D bash-language-server", LocalNPM: true},
	}
}

func availableChecks(defs []toolDef) []checkItem {
	checks := make([]checkItem, 0, len(defs))
	for _, def := range defs {
		path, ok := resolveTool(def)
		checks = append(checks, checkItem{Name: def.Name, Language: def.Language, Available: ok, Path: path, Env: def.Env, Install: def.Install})
	}
	sort.Slice(checks, func(i, j int) bool {
		if checks[i].Language == checks[j].Language {
			return checks[i].Name < checks[j].Name
		}
		return checks[i].Language < checks[j].Language
	})
	return checks
}

func checksForGroups(groups fileGroups) []checkItem {
	langs := map[string]bool{}
	if len(groups.Go) > 0 {
		langs["go"] = true
	}
	if len(groups.JS) > 0 {
		langs["javascript"] = true
	}
	if len(groups.Bash) > 0 {
		langs["bash"] = true
	}
	defs := make([]toolDef, 0)
	for _, def := range allToolDefs() {
		if langs[def.Language] {
			defs = append(defs, def)
		}
	}
	return availableChecks(defs)
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

func toolDefByName(name string) toolDef {
	for _, def := range allToolDefs() {
		if def.Name == name {
			return def
		}
	}
	return toolDef{Name: name, Env: "TASTE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))}
}
func runTaste(opts options) result {
	res := result{SchemaVersion: 1, Scope: opts.Scope, Level: opts.Level, Checks: []checkItem{}, Fixed: []fixedItem{}, Issues: []issueItem{}, Warnings: []warningItem{}, Commands: []commandItem{}}
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
		res.Issues = append(res.Issues, issueItem{Severity: "error", Message: err.Error()})
		return finalize(res, opts.MaxIssues)
	}
	groups := classifyFiles(paths)
	res.Checks = checksForGroups(groups)

	format := opts.Intent == "fix"
	diag := opts.Intent == "check" || opts.Intent == "fix"

	if len(groups.Go) > 0 {
		runGo(&res, groups.Go, format, diag, res.Level)
	}
	if len(groups.JS) > 0 {
		runJS(&res, groups.JS, format, diag, res.Level)
	}
	if len(groups.Bash) > 0 {
		runBash(&res, groups.Bash, format, diag, res.Level)
	}
	if len(paths) == 0 || (len(groups.Go) == 0 && len(groups.JS) == 0 && len(groups.Bash) == 0) {
		res.Warnings = append(res.Warnings, warningItem{Message: "no supported source files matched scope"})
	}
	return finalize(res, opts.MaxIssues)
}

func finalize(res result, maxIssues int) result {
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
	if res.TotalIssues == 0 {
		res.Status = "pass"
		res.Summary = fmt.Sprintf("PASS fixed: %s; remaining: 0", fixedSummary(res.Fixed))
		return res
	}
	res.Status = "fail"
	res.Summary = fmt.Sprintf("FAIL %d issues, %d warnings", res.TotalIssues, len(res.Warnings))
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
		return nil, errors.New("git changed-file detection failed")
	}
	return splitGitFileList(out), nil
}

func gitUntrackedFiles() ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("git untracked-file detection failed")
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

func classifyFiles(paths []string) fileGroups {
	var groups fileGroups
	for _, p := range paths {
		switch {
		case isGoFile(p):
			groups.Go = append(groups.Go, p)
		case isJSFile(p):
			groups.JS = append(groups.JS, p)
		case isBashFile(p):
			groups.Bash = append(groups.Bash, p)
		}
	}
	return groups
}

func isKnownSource(p string) bool { return isGoFile(p) || isJSFile(p) || isBashFile(p) }
func isGoFile(p string) bool      { return strings.HasSuffix(p, ".go") }
func isJSFile(p string) bool {
	ext := filepath.Ext(p)
	return ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts" || ext == ".mjs" || ext == ".cjs"
}
func isBashFile(p string) bool {
	ext := filepath.Ext(p)
	return ext == ".sh" || ext == ".bash" || ext == ".zsh"
}

func runGo(res *result, files []string, format, diag bool, level string) {
	if format {
		if _, ok := resolveTool(toolDefByName("gofmt")); !ok {
			res.Warnings = append(res.Warnings, warningItem{Language: "go", Message: "gofmt not found; override with TASTE_GOFMT"})
		} else {
			cmd := append([]string{"-w"}, files...)
			status, summary := runExternal("gofmt", cmd...)
			res.Commands = append(res.Commands, commandItem{Name: "gofmt", Status: status, Summary: summary})
			if status == "pass" {
				res.Fixed = append(res.Fixed, fixedItem{Language: "go", Kind: "format", Files: len(files)})
			} else {
				res.Issues = append(res.Issues, issueItem{Language: "go", Severity: "error", Code: "gofmt", Message: summary})
			}
		}
	}
	if !diag {
		return
	}
	root := findWorkspaceRoot(files)
	issues, summary, err := runGoplsDiagnostics(root, files)
	if err != nil {
		res.Warnings = append(res.Warnings, warningItem{Language: "go", Message: err.Error()})
	} else {
		status := "pass"
		if len(issues) > 0 {
			status = "fail"
		}
		if summary == "" {
			summary = fmt.Sprintf("%d diagnostics", len(issues))
		}
		res.Commands = append(res.Commands, commandItem{Name: "gopls", Status: status, Summary: summary})
		res.Issues = append(res.Issues, issues...)
	}
	if !format {
		status, summary := runExternal("gofmt", append([]string{"-l"}, files...)...)
		res.Commands = append(res.Commands, commandItem{Name: "gofmt -l", Status: status, Summary: summary})
		if status == "pass" && strings.TrimSpace(summary) != "" {
			for _, f := range strings.Fields(summary) {
				res.Issues = append(res.Issues, issueItem{Language: "go", Severity: "error", File: f, Code: "gofmt", Message: "file is not formatted"})
			}
		} else if status == "fail" {
			res.Issues = append(res.Issues, issueItem{Language: "go", Severity: "error", Code: "gofmt", Message: summary})
		}
	}
	if level == "strict" && fileExists(filepath.Join(root, "go.mod")) {
		for _, spec := range []struct {
			name string
			args []string
		}{{"go test", []string{"test", "./..."}}, {"go vet", []string{"vet", "./..."}}} {
			status, summary := runExternalInDir(root, "go", spec.args...)
			res.Commands = append(res.Commands, commandItem{Name: spec.name, Status: status, Summary: summary})
			if status == "fail" {
				res.Issues = append(res.Issues, issueItem{Language: "go", Severity: "error", Code: spec.name, Message: summary})
			}
		}
	}
}

func runJS(res *result, files []string, format, diag bool, level string) {
	root := findWorkspaceRoot(files)
	if diag {
		issues, summary, err := runTypeScriptDiagnostics(root, files)
		if err != nil {
			res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: err.Error()})
		} else {
			status := "pass"
			if len(issues) > 0 {
				status = "fail"
			}
			if summary == "" {
				summary = fmt.Sprintf("%d diagnostics", len(issues))
			}
			res.Commands = append(res.Commands, commandItem{Name: "typescript-language-server", Status: status, Summary: summary})
			res.Issues = append(res.Issues, issues...)
		}
	}

	scripts := packageScripts(root)
	if len(scripts) == 0 {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "package.json scripts not found"})
		return
	}
	if _, ok := resolveToolInDir(toolDefByName("npm"), root); !ok {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "npm not found; override with TASTE_NPM"})
		return
	}
	if format {
		runNPMScript(res, root, scripts, "format", true)
	}
	if diag {
		runNPMScript(res, root, scripts, "lint", true)
		if level == "strict" {
			runNPMScript(res, root, scripts, "test", true)
		}
	}
}

func runNPMScript(res *result, dir string, scripts map[string]bool, script string, issueOnFail bool) {
	if !scripts[script] {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "npm script missing: " + script})
		return
	}
	status, summary := runExternalInDir(dir, "npm", "run", script)
	res.Commands = append(res.Commands, commandItem{Name: "npm run " + script, Status: status, Summary: summary})
	if status == "fail" && issueOnFail {
		res.Issues = append(res.Issues, issueItem{Language: "javascript", Severity: "error", Code: "npm run " + script, Message: summary})
	}
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

func runBash(res *result, files []string, format, diag bool, level string) {
	if format {
		res.Warnings = append(res.Warnings, warningItem{Language: "bash", Message: "bash formatting not configured"})
	}
	if !diag {
		return
	}
	root := findWorkspaceRoot(files)
	issues, summary, err := runBashLanguageDiagnostics(root, files)
	if err != nil {
		res.Warnings = append(res.Warnings, warningItem{Language: "bash", Message: err.Error()})
	} else {
		status := "pass"
		if len(issues) > 0 {
			status = "fail"
		}
		if summary == "" {
			summary = fmt.Sprintf("%d diagnostics", len(issues))
		}
		res.Commands = append(res.Commands, commandItem{Name: "bash-language-server", Status: status, Summary: summary})
		res.Issues = append(res.Issues, issues...)
	}
	for _, f := range files {
		status, summary := runExternal("bash", "-n", f)
		res.Commands = append(res.Commands, commandItem{Name: "bash -n " + f, Status: status, Summary: summary})
		if status == "fail" {
			res.Issues = append(res.Issues, issueItem{Language: "bash", Severity: "error", File: f, Code: "bash -n", Message: summary})
		}
	}
	if level != "strict" {
		return
	}
	if _, ok := resolveTool(toolDefByName("shellcheck")); !ok {
		res.Warnings = append(res.Warnings, warningItem{Language: "bash", Message: "shellcheck not found; override with TASTE_SHELLCHECK"})
		return
	}
	for _, f := range files {
		status, summary := runExternal("shellcheck", f)
		res.Commands = append(res.Commands, commandItem{Name: "shellcheck " + f, Status: status, Summary: summary})
		if status == "fail" {
			res.Issues = append(res.Issues, issueItem{Language: "bash", Severity: "error", File: f, Code: "shellcheck", Message: summary})
		}
	}
}

func runExternal(name string, args ...string) (string, string) {
	return runExternalInDir(".", name, args...)
}

func runExternalInDir(dir, name string, args ...string) (string, string) {
	path, ok := resolveToolInDir(toolDefByName(name), dir)
	if !ok {
		return "fail", fmt.Sprintf("%s not found; override with %s", name, toolDefByName(name).Env)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	summary := summarizeOutput(out)
	if err != nil {
		if summary == "" {
			summary = err.Error()
		}
		return "fail", summary
	}
	return "pass", summary
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
