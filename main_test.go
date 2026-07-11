package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func captureRun(args []string, stdin string) (int, string, string) {
	var out, err bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &err)
	return code, out.String(), err.String()
}

func TestVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		code, out, errOut := captureRun(args, "")
		if code != 0 {
			t.Fatalf("version failed args=%v err=%s", args, errOut)
		}
		if strings.TrimSpace(out) != "taste "+version {
			t.Fatalf("version output = %q", out)
		}
	}
}

func TestParseArgs(t *testing.T) {
	opts, err := parseArgs([]string{"main.go", "scripts/dev.sh", "--json"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Intent != "check" || opts.Level != "easy" || opts.Scope != "paths" || !opts.JSONOut || len(opts.Paths) != 2 {
		t.Fatalf("unexpected opts: %#v", opts)
	}
}

func TestCheckLevelArgs(t *testing.T) {
	opts, err := parseArgs([]string{"main.go", "--strict"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Intent != "check" || opts.Level != "strict" || opts.Scope != "paths" || len(opts.Paths) != 1 {
		t.Fatalf("unexpected strict opts: %#v", opts)
	}
	opts, err = parseArgs([]string{"main.go", "--fix"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Intent != "fix" || opts.Level != "easy" {
		t.Fatalf("unexpected fix opts: %#v", opts)
	}
}

func TestStdinJSONArgs(t *testing.T) {
	opts, err := parseArgs([]string{"--stdin-json", "--json"}, strings.NewReader(`{"paths":["main.go"],"scope":"paths"}`))
	if err != nil {
		t.Fatal(err)
	}
	if opts.Intent != "check" || opts.Scope != "paths" || !opts.JSONOut || len(opts.Paths) != 1 || opts.Paths[0] != "main.go" {
		t.Fatalf("unexpected opts: %#v", opts)
	}
}

func TestClassifyFiles(t *testing.T) {
	flavors, err := loadFlavors()
	if err != nil {
		t.Fatal(err)
	}
	groups := classifyByFlavor([]string{"main.go", "app.ts", "script.sh", "README.md"}, flavors)
	if len(groups["go"]) != 1 || len(groups["javascript"]) != 1 || len(groups["bash"]) != 1 {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if _, ok := groups["README.md"]; ok {
		t.Fatalf("unmatched extension should not produce a group: %#v", groups)
	}
}

func TestJSONNoFilesScope(t *testing.T) {
	code, out, errOut := captureRun([]string{"README.md", "--json"}, "")
	if code != 0 {
		t.Fatalf("no source files should pass with warning only, err=%s", errOut)
	}
	var payload result
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "pass" || payload.Scope != "paths" || len(payload.Warnings) == 0 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestFlavorsJSON(t *testing.T) {
	code, out, errOut := captureRun([]string{"--flavors", "--json"}, "")
	if code != 0 {
		t.Fatalf("flavors failed: %s", errOut)
	}
	var payload flavorsResult
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != schemaVersion || payload.Status != "ok" || len(payload.Checks) == 0 {
		t.Fatalf("unexpected flavors payload: %#v", payload)
	}
}

func TestParseArgsSeparatorAndMaxIssues(t *testing.T) {
	opts, err := parseArgs([]string{"--json", "--max-issues", "3", "--", "-flaggy.go"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSONOut || opts.MaxIssues != 3 || opts.Scope != "paths" || len(opts.Paths) != 1 || opts.Paths[0] != "-flaggy.go" {
		t.Fatalf("unexpected opts: %#v", opts)
	}

	opts, err = parseArgs([]string{"--max-issues=7", "main.go"}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxIssues != 7 {
		t.Fatalf("unexpected max issues: %#v", opts)
	}
}

func TestParseArgsRejectProjectFix(t *testing.T) {
	if _, err := parseArgs([]string{"--project", "--fix"}, strings.NewReader("")); err == nil {
		t.Fatal("expected --project --fix rejection")
	}
}

func TestParseArgsRejectStrictFix(t *testing.T) {
	if _, err := parseArgs([]string{"--strict", "--fix"}, strings.NewReader("")); err == nil {
		t.Fatal("expected --strict --fix rejection: --fix mutates only and does not diagnose")
	}
}

func TestFinalizeCapsIssuesAndSetsSchema(t *testing.T) {
	res := finalize(result{SchemaVersion: 1, Issues: []issueItem{
		{Severity: "warning", File: "b.go", Message: "warn"},
		{Severity: "error", File: "a.go", Message: "err 1"},
		{Severity: "error", File: "c.go", Message: "err 2"},
	}}, 2)
	if res.SchemaVersion != 1 || res.Status != "fail" || res.TotalIssues != 3 || len(res.Issues) != 2 {
		t.Fatalf("unexpected capped result: %#v", res)
	}
	if res.Issues[0].File != "a.go" || res.Issues[1].File != "c.go" {
		t.Fatalf("issues not sorted before cap: %#v", res.Issues)
	}
}
func TestChecksHeader(t *testing.T) {
	checks := checksForFlavorNames(map[string]bool{"go": true})
	res := finalize(result{SchemaVersion: 1, Checks: checks}, defaultMaxIssues)
	if !strings.Contains(res.Header, "gofmt") || !strings.HasPrefix(res.Header, "checks:") {
		t.Fatalf("unexpected header: %q", res.Header)
	}
}
