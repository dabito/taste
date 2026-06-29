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
