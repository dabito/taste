package main

import (
	"bytes"
	"encoding/json"
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
	if !hasIssue(payload.Issues, "testdata/bad/go/type-error/main.go", "compiler:IncompatibleAssign", "cannot use 1") {
		t.Fatalf("missing incompatible assign diagnostic: %#v", payload.Issues)
	}
	if !hasIssue(payload.Issues, "testdata/bad/go/type-error/main.go", "compiler:UnusedVar", "declared and not used") {
		t.Fatalf("missing unused var diagnostic: %#v", payload.Issues)
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
	if !hasIssueCodeContains(payload.Issues, "testdata/bad/js/type-error/main.ts", "2322", "not assignable") {
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
	if !hasIssue(payload.Issues, "testdata/bad/bash/syntax-error/script.sh", "bash -n", "syntax error") {
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
	var out, errOut bytes.Buffer
	code := run([]string{"testdata/bad/js/failing-test/main.js", "--strict", "--json"}, strings.NewReader(""), &out, &errOut)
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

func hasIssueCodeContains(issues []issueItem, file, codePart, message string) bool {
	for _, issue := range issues {
		if issue.File == file && strings.Contains(issue.Code, codePart) && strings.Contains(issue.Message, message) {
			return true
		}
	}
	return false
}
