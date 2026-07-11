package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticFlavor is a fake, test-only language flavor: .toy files, a
// fixable "toyfmt" tool, and an LSP-kind "toylsp" diagnostic tool with no
// corresponding fix step. It exists so generic engine-mechanism tests
// (autofix/diagnose decoupling, incomplete-tool handling, unfixable-tool
// warnings) don't depend on the real go/js/bash config -- those tests
// should keep passing even if gofmt/gopls/eslint details change, since the
// mechanism under test has nothing to do with any real language.
func syntheticFlavor() flavorDef {
	return flavorDef{
		Name:       "toylang",
		Extensions: []string{".toy"},
		Tools: []flavorTool{
			{Name: "toyfmt", Env: "TASTE_TOYFMT"},
			{Name: "toylsp", Kind: "lsp", Env: "TASTE_TOYLSP", IssueLanguage: "toylang", LanguageID: "toylang"},
		},
		Actions: flavorActions{
			Fix: flavorAction{Steps: []flavorStep{
				{Tool: "toyfmt", Kind: "argv", Args: []string{"-l", "-w", "{files}"}, Fixable: true},
			}},
			Taste: flavorAction{Steps: []flavorStep{
				{Tool: "toylsp", Kind: "lsp"},
			}},
		},
	}
}

func TestEngineIncompleteWhenRequiredToolMissing(t *testing.T) {
	t.Setenv("TASTE_TOYLSP", "/nonexistent/toylsp")
	dir := t.TempDir()
	path := filepath.Join(dir, "main.toy")
	if err := os.WriteFile(path, []byte("toy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := runWithFlavors([]string{path, "--json"}, strings.NewReader(""), &out, &errOut, []flavorDef{syntheticFlavor()})
	if code != 3 {
		t.Fatalf("expected incomplete exit code 3, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "incomplete" {
		t.Fatalf("expected status=incomplete: %#v", payload)
	}
	if len(payload.Issues) != 0 {
		t.Fatalf("expected no confirmed issues, only an unavailable tool: %#v", payload.Issues)
	}
	found := false
	for _, item := range payload.Incomplete {
		if item.Tool == "toylsp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected toylsp listed in incomplete: %#v", payload.Incomplete)
	}
}

func TestEngineAutofixDoesNotDiagnoseAndWarnsUnfixable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.toy")
	if err := os.WriteFile(path, []byte("toy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixScript := filepath.Join(dir, "toyfmt")
	if err := os.WriteFile(fixScript, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASTE_TOYFMT", fixScript)
	t.Setenv("TASTE_TOYLSP", "/nonexistent/toylsp") // must never be invoked by --autofix

	var out, errOut bytes.Buffer
	code := runWithFlavors([]string{path, "--autofix", "--json"}, strings.NewReader(""), &out, &errOut, []flavorDef{syntheticFlavor()})
	if code != 0 {
		t.Fatalf("expected a clean autofix run, code=%d stderr=%s stdout=%s", code, errOut.String(), out.String())
	}
	var payload result
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON, code=%d stderr=%s stdout=%s err=%v", code, errOut.String(), out.String(), err)
	}
	for _, cmd := range payload.Commands {
		if cmd.Name == "toylsp" {
			t.Fatalf("--autofix should not run diagnostics (found toylsp command): %#v", payload.Commands)
		}
	}
	if len(payload.Issues) != 0 {
		t.Fatalf("--autofix should not report diagnostic issues: %#v", payload.Issues)
	}
	if strings.Contains(payload.Summary, "remaining: 0") {
		t.Fatalf("--autofix summary should not claim a verified remaining count it never checked: %q", payload.Summary)
	}
	if !strings.Contains(payload.Summary, "not diagnosed") {
		t.Fatalf("--autofix summary should make clear diagnostics did not run: %q", payload.Summary)
	}
	found := false
	for _, w := range payload.Warnings {
		if strings.Contains(w.Message, "toylsp") && strings.Contains(w.Message, "no fix step configured") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning naming toylsp as unfixable by --autofix: %#v", payload.Warnings)
	}
}
