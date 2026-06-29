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
	groups := classifyFiles([]string{"main.go", "app.ts", "script.sh", "README.md"})
	if len(groups.Go) != 1 || len(groups.JS) != 1 || len(groups.Bash) != 1 {
		t.Fatalf("unexpected groups: %#v", groups)
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
	if payload.Status != "ok" || len(payload.Checks) == 0 {
		t.Fatalf("unexpected flavors payload: %#v", payload)
	}
}

func TestChecksHeader(t *testing.T) {
	groups := fileGroups{Go: []string{"main.go"}}
	checks := checksForGroups(groups)
	res := finalize(result{Checks: checks})
	if !strings.Contains(res.Header, "gofmt") || !strings.HasPrefix(res.Header, "checks:") {
		t.Fatalf("unexpected header: %q", res.Header)
	}
}
