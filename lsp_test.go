package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLSPTimeoutForLevel(t *testing.T) {
	if got := lspTimeoutForLevel("easy"); got != defaultLSPTimeoutEasy {
		t.Fatalf("easy default = %v, want %v", got, defaultLSPTimeoutEasy)
	}
	if got := lspTimeoutForLevel("strict"); got != defaultLSPTimeoutStrict {
		t.Fatalf("strict default = %v, want %v", got, defaultLSPTimeoutStrict)
	}
	if defaultLSPTimeoutStrict <= defaultLSPTimeoutEasy {
		t.Fatalf("strict default (%v) should be longer than easy default (%v)", defaultLSPTimeoutStrict, defaultLSPTimeoutEasy)
	}

	t.Setenv("TASTE_LSP_TIMEOUT", "20s")
	if got := lspTimeoutForLevel("easy"); got != 20*time.Second {
		t.Fatalf("TASTE_LSP_TIMEOUT override = %v, want 20s", got)
	}
	if got := lspTimeoutForLevel("strict"); got != 20*time.Second {
		t.Fatalf("TASTE_LSP_TIMEOUT override should apply to strict too, got %v", got)
	}

	t.Setenv("TASTE_LSP_TIMEOUT", "not-a-duration")
	if got := lspTimeoutForLevel("easy"); got != defaultLSPTimeoutEasy {
		t.Fatalf("malformed TASTE_LSP_TIMEOUT should fall back to default, got %v", got)
	}

	t.Setenv("TASTE_LSP_TIMEOUT", "-5s")
	if got := lspTimeoutForLevel("strict"); got != defaultLSPTimeoutStrict {
		t.Fatalf("non-positive TASTE_LSP_TIMEOUT should fall back to default, got %v", got)
	}
}

// TestFindWorkspaceRootFallsBackToFileDirNotCallerCwd guards against a real
// bug: a lone ad-hoc file with no go.mod/package.json/tsconfig.json/.git
// anywhere above it used to make findWorkspaceRoot fall back to the
// *calling process's* cwd, which has nothing to do with where the file
// lives. That silently fed gopls the wrong workspace and made it miss real
// diagnostics rather than error. The correct fallback is the file's own
// directory, regardless of where taste was invoked from.
func TestFindWorkspaceRootFallsBackToFileDirNotCallerCwd(t *testing.T) {
	adHocDir := t.TempDir()
	filePath := filepath.Join(adHocDir, "main.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second, unrelated directory that DOES have a project marker,
	// standing in for "whatever repo the caller happens to be sitting in."
	callerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(callerDir, "go.mod"), []byte("module caller\n"), 0o644); err != nil {
		t.Fatal(err)
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
	if err := os.Chdir(callerDir); err != nil {
		t.Fatal(err)
	}

	root := findWorkspaceRoot([]string{filePath})
	want, err := filepath.Abs(adHocDir)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("findWorkspaceRoot(%q) = %q, want the file's own directory %q (not caller cwd %q)", filePath, root, want, callerDir)
	}
}
