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
	"strings"
)

const version = "0.1.0-beta.1"

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
	Message  string `json:"message"`
}

type warningItem struct {
	Language string `json:"language,omitempty"`
	Message  string `json:"message"`
}

type commandItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type result struct {
	Status   string        `json:"status"`
	Scope    string        `json:"scope"`
	Summary  string        `json:"summary"`
	Fixed    []fixedItem   `json:"fixed"`
	Issues   []issueItem   `json:"issues"`
	Warnings []warningItem `json:"warnings"`
	Commands []commandItem `json:"commands"`
}

type options struct {
	Intent  string
	Scope   string
	Paths   []string
	JSONOut bool
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

type cliError string

func (e cliError) Error() string { return string(e) }

func usage() {
	fmt.Fprintln(os.Stderr, `usage: taste <check|fix|format|gate|version> [scope] [--json]

Scopes:
  --changed            changed files from git
  --project            whole project
  --paths <files...>   explicit files
  --stdin-json         read {"paths":[...]} from stdin

Examples:
  taste gate --changed
  taste fix --paths main.go scripts/dev.sh --json
  taste version`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usageTo(stderr)
		return 2
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "taste %s\n", version)
		return 0
	case "help", "--help", "-h":
		usageTo(stdout)
		return 0
	}

	opts, err := parseArgs(args, stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		usageTo(stderr)
		return 2
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
	fmt.Fprintln(w, `usage: taste <check|fix|format|gate|version> [scope] [--json]

Scopes:
  --changed            changed files from git
  --project            whole project
  --paths <files...>   explicit files
  --stdin-json         read {"paths":[...]} from stdin

Examples:
  taste gate --changed
  taste fix --paths main.go scripts/dev.sh --json
  taste version`)
}

func parseArgs(args []string, stdin io.Reader) (options, error) {
	intent := args[0]
	if intent != "check" && intent != "fix" && intent != "format" && intent != "gate" {
		return options{}, cliError("unknown command: " + intent)
	}
	opts := options{Intent: intent}
	collectPaths := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if collectPaths {
			if arg == "--json" {
				opts.JSONOut = true
				continue
			}
			if strings.HasPrefix(arg, "--") {
				return options{}, cliError("unknown flag after --paths: " + arg)
			}
			opts.Paths = append(opts.Paths, arg)
			continue
		}
		switch arg {
		case "--json":
			opts.JSONOut = true
		case "--changed":
			opts.Scope = "changed"
		case "--project":
			opts.Scope = "project"
		case "--paths":
			opts.Scope = "paths"
			collectPaths = true
		case "--stdin-json":
			opts.Scope = "stdin-json"
			payload, err := readStdinPayload(stdin)
			if err != nil {
				return options{}, err
			}
			if payload.Scope != "" {
				opts.Scope = payload.Scope
			}
			opts.Paths = append(opts.Paths, payload.Paths...)
		default:
			if strings.HasPrefix(arg, "--") {
				return options{}, cliError("unknown flag: " + arg)
			}
			return options{}, cliError("unexpected argument: " + arg)
		}
	}
	if opts.Scope == "paths" && len(opts.Paths) == 0 {
		return options{}, cliError("--paths needs at least one file")
	}
	return opts, nil
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

func runTaste(opts options) result {
	res := result{Scope: opts.Scope, Fixed: []fixedItem{}, Issues: []issueItem{}, Warnings: []warningItem{}, Commands: []commandItem{}}
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
		return finalize(res)
	}
	groups := classifyFiles(paths)

	format := opts.Intent == "format" || opts.Intent == "fix" || opts.Intent == "gate"
	diag := opts.Intent == "check" || opts.Intent == "fix" || opts.Intent == "gate"

	if len(groups.Go) > 0 {
		runGo(&res, groups.Go, format, diag)
	}
	if len(groups.JS) > 0 {
		runJS(&res, format, diag)
	}
	if len(groups.Bash) > 0 {
		runBash(&res, groups.Bash, format, diag)
	}
	if len(paths) == 0 || (len(groups.Go) == 0 && len(groups.JS) == 0 && len(groups.Bash) == 0) {
		res.Warnings = append(res.Warnings, warningItem{Message: "no supported source files matched scope"})
	}
	return finalize(res)
}

func finalize(res result) result {
	if len(res.Issues) == 0 {
		res.Status = "pass"
		res.Summary = fmt.Sprintf("PASS fixed: %s; remaining: 0", fixedSummary(res.Fixed))
		return res
	}
	res.Status = "fail"
	res.Summary = fmt.Sprintf("FAIL %d issues, %d warnings", len(res.Issues), len(res.Warnings))
	return res
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
		return cleanPaths(paths), nil
	case "changed":
		return gitChangedFiles()
	case "project":
		return projectFiles(".")
	default:
		if len(paths) > 0 {
			return cleanPaths(paths), nil
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

func gitChangedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", "HEAD", "--")
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New("git changed-file detection failed")
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	return cleanPaths(lines), nil
}

func projectFiles(root string) ([]string, error) {
	var paths []string
	skipDirs := map[string]bool{".git": true, "node_modules": true, "dist": true, "build": true, "coverage": true, "vendor": true}
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
	return ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".mjs" || ext == ".cjs"
}
func isBashFile(p string) bool {
	ext := filepath.Ext(p)
	return ext == ".sh" || ext == ".bash" || ext == ".zsh"
}

func runGo(res *result, files []string, format, diag bool) {
	if format {
		if _, err := exec.LookPath("gofmt"); err != nil {
			res.Warnings = append(res.Warnings, warningItem{Language: "go", Message: "gofmt not found"})
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
	if fileExists("go.mod") {
		for _, spec := range []struct {
			name string
			args []string
		}{{"go test", []string{"test", "./..."}}, {"go vet", []string{"vet", "./..."}}} {
			status, summary := runExternal("go", spec.args...)
			res.Commands = append(res.Commands, commandItem{Name: spec.name, Status: status, Summary: summary})
			if status == "fail" {
				res.Issues = append(res.Issues, issueItem{Language: "go", Severity: "error", Code: spec.name, Message: summary})
			}
		}
	}
}

func runJS(res *result, format, diag bool) {
	scripts := packageScripts()
	if len(scripts) == 0 {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "package.json scripts not found"})
		return
	}
	if _, err := exec.LookPath("npm"); err != nil {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "npm not found"})
		return
	}
	if format {
		runNPMScript(res, scripts, "format", true)
	}
	if diag {
		runNPMScript(res, scripts, "lint", true)
		runNPMScript(res, scripts, "test", true)
	}
}

func runNPMScript(res *result, scripts map[string]bool, script string, issueOnFail bool) {
	if !scripts[script] {
		res.Warnings = append(res.Warnings, warningItem{Language: "javascript", Message: "npm script missing: " + script})
		return
	}
	status, summary := runExternal("npm", "run", script)
	res.Commands = append(res.Commands, commandItem{Name: "npm run " + script, Status: status, Summary: summary})
	if status == "fail" && issueOnFail {
		res.Issues = append(res.Issues, issueItem{Language: "javascript", Severity: "error", Code: "npm run " + script, Message: summary})
	}
}

func packageScripts() map[string]bool {
	data, err := os.ReadFile("package.json")
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

func runBash(res *result, files []string, format, diag bool) {
	if format {
		res.Warnings = append(res.Warnings, warningItem{Language: "bash", Message: "bash formatting not configured"})
	}
	if !diag {
		return
	}
	for _, f := range files {
		status, summary := runExternal("bash", "-n", f)
		res.Commands = append(res.Commands, commandItem{Name: "bash -n " + f, Status: status, Summary: summary})
		if status == "fail" {
			res.Issues = append(res.Issues, issueItem{Language: "bash", Severity: "error", File: f, Code: "bash -n", Message: summary})
		}
	}
	if _, err := exec.LookPath("shellcheck"); err != nil {
		res.Warnings = append(res.Warnings, warningItem{Language: "bash", Message: "shellcheck not found"})
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
	cmd := exec.Command(name, args...)
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
