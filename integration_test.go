package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTasteGoplsDiagnosticsFixture(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/go/type-error/main.go", "--json"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Fatalf("expected failing diagnostics, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "fail" || payload.Level != "easy" {
		t.Fatalf("unexpected payload status/level: %#v", payload)
	}
	if !hasCommand(payload.Commands, "gopls", "fail") {
		t.Fatalf("missing failing gopls command: %#v", payload.Commands)
	}
	// Assert on Code, gopls/go-types' own stable structured diagnostic
	// identifier, not on the compiler's prose wording -- that wording isn't
	// part of any stability contract and a future Go release rephrasing it
	// shouldn't fail this test. Message is checked only for non-emptiness.
	assign, ok := findIssue(payload.Issues, "testdata/bad/go/type-error/main.go", "compiler:IncompatibleAssign")
	if !ok || assign.Message == "" {
		t.Fatalf("missing incompatible assign diagnostic: %#v", payload.Issues)
	}
	unused, ok := findIssue(payload.Issues, "testdata/bad/go/type-error/main.go", "compiler:UnusedVar")
	if !ok || unused.Message == "" {
		t.Fatalf("missing unused var diagnostic: %#v", payload.Issues)
	}
}

// TestTasteIncompleteWhenRequiredToolMissing and TestTasteFixDoesNotDiagnose
// (generic engine mechanisms) now live in engine_test.go against a
// synthetic flavor, decoupled from the real go/js/bash config -- see
// syntheticFlavor there for why.

func TestTasteChangedFallsBackToProjectOnEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %s: %v", out, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run([]string{"--json"}, strings.NewReader(""), &out, &errOut)
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON, code=%d stderr=%s stdout=%s err=%v", code, errOut.String(), out.String(), err)
	}
	if payload.Scope != "project" {
		t.Fatalf("expected fallback to --project scope on empty repo, no commits yet: %#v", payload)
	}
	found := false
	for _, w := range payload.Warnings {
		if strings.Contains(w.Message, "git changed-file detection failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning explaining the git-state fallback: %#v", payload.Warnings)
	}
}

func TestTasteTypeScriptDiagnosticsFixture(t *testing.T) {
	if _, ok := resolveTool(toolDefByName("typescript-language-server")); !ok {
		t.Skip("typescript-language-server not installed")
	}
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/js/type-error/main.ts", "--json"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Fatalf("expected failing diagnostics, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "fail" || payload.Level != "easy" {
		t.Fatalf("unexpected payload status/level: %#v", payload)
	}
	if !hasCommand(payload.Commands, "typescript-language-server", "fail") {
		t.Fatalf("missing failing typescript-language-server command: %#v", payload.Commands)
	}
	// 2322 is TypeScript's own stable numeric diagnostic code; the message
	// wording ("not assignable") isn't part of any stability contract, so
	// only check it's non-empty, not its exact phrasing.
	assignability, ok := findIssueCodeContains(payload.Issues, "testdata/bad/js/type-error/main.ts", "2322")
	if !ok || assignability.Message == "" {
		t.Fatalf("missing TypeScript assignability diagnostic: %#v", payload.Issues)
	}
}

func TestTasteBashSyntaxFixture(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/bash/syntax-error/script.sh", "--json"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Fatalf("expected failing diagnostics, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "fail" || payload.Level != "easy" {
		t.Fatalf("unexpected payload status/level: %#v", payload)
	}
	if !hasCommand(payload.Commands, "bash -n testdata/bad/bash/syntax-error/script.sh", "fail") {
		t.Fatalf("missing failing bash -n command: %#v", payload.Commands)
	}
	// "bash -n" here is our own step name (Code), not a bash diagnostic
	// code -- bash has no structured error codes, only prose. Check the
	// message is non-empty rather than pinning to bash's exact wording.
	syntaxErr, ok := findIssue(payload.Issues, "testdata/bad/bash/syntax-error/script.sh", "bash -n")
	if !ok || syntaxErr.Message == "" {
		t.Fatalf("missing bash syntax diagnostic: %#v", payload.Issues)
	}
}

func TestTasteStrictGoUsesTargetWorkspace(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/go/failing-test", "--strict", "--json"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Fatalf("expected failing target-root go test, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(payload.Commands, "go test", "fail") {
		t.Fatalf("missing target-root go test failure: %#v", payload.Commands)
	}
	if !hasIssue(payload.Issues, "", "go test", "target-root go test fixture") {
		t.Fatalf("missing target-root go test issue: %#v", payload.Issues)
	}
}

func TestTasteStrictJSUsesTargetWorkspace(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}
	if _, ok := resolveTool(toolDefByName("typescript-language-server")); !ok {
		t.Skip("typescript-language-server not installed")
	}
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/js/failing-test/main.js", "--strict", "--allow-scripts", "--json"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Fatalf("expected failing target-root npm test, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(payload.Commands, "npm run test", "fail") {
		t.Fatalf("missing target-root npm test failure: %#v", payload.Commands)
	}
}

func TestTasteJSScriptsGatedByDefault(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/js/failing-test/main.js", "--strict", "--json"}, strings.NewReader(""), &out, &errOut)
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON, code=%d stderr=%s stdout=%s err=%v", code, errOut.String(), out.String(), err)
	}
	if !hasCommand(payload.Commands, "npm run test", "skip") {
		t.Fatalf("expected npm run test withheld by default without --allow-scripts: %#v", payload.Commands)
	}
	found := false
	for _, w := range payload.Warnings {
		if strings.Contains(w.Message, "allow-scripts") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning explaining how to allow the withheld script: %#v", payload.Warnings)
	}
}

func TestExplicitTestdataTargetIsScanned(t *testing.T) {
	files, err := targetFiles([]string{"testdata/bad/bash"})
	if err != nil {
		t.Fatal(err)
	}
	want := "testdata/bad/bash/syntax-error/script.sh"
	for _, file := range files {
		if file == want {
			return
		}
	}
	t.Fatalf("explicit testdata target missing %s in %#v", want, files)
}
func hasCommand(commands []commandItem, name, status string) bool {
	for _, command := range commands {
		if command.Name == name && command.Status == status {
			return true
		}
	}
	return false
}

func hasIssue(issues []issueItem, file, code, message string) bool {
	for _, issue := range issues {
		if issue.File == file && issue.Code == code && strings.Contains(issue.Message, message) {
			return true
		}
	}
	return false
}

// findIssue and findIssueCodeContains return the matched issue (not just a
// bool) so callers can assert on its Message separately from finding it --
// see the comment at their call sites for why that split matters.
func findIssue(issues []issueItem, file, code string) (issueItem, bool) {
	for _, issue := range issues {
		if issue.File == file && issue.Code == code {
			return issue, true
		}
	}
	return issueItem{}, false
}

func findIssueCodeContains(issues []issueItem, file, codePart string) (issueItem, bool) {
	for _, issue := range issues {
		if issue.File == file && strings.Contains(issue.Code, codePart) {
			return issue, true
		}
	}
	return issueItem{}, false
}
