package main

import "testing"

func TestLoadFlavorsMatchesHardcodedDefaults(t *testing.T) {
	flavors, err := loadFlavors()
	if err != nil {
		t.Fatal(err)
	}
	if len(flavors) != 3 {
		t.Fatalf("expected 3 built-in flavors, got %d: %#v", len(flavors), flavors)
	}

	byName := map[string]flavorDef{}
	for _, fl := range flavors {
		byName[fl.Name] = fl
	}

	goFl, ok := byName["go"]
	if !ok {
		t.Fatal("missing go flavor")
	}
	if len(goFl.Extensions) != 1 || goFl.Extensions[0] != ".go" {
		t.Fatalf("unexpected go extensions: %#v", goFl.Extensions)
	}
	if len(goFl.RootMarkers) != 1 || goFl.RootMarkers[0] != "go.mod" {
		t.Fatalf("unexpected go root markers: %#v", goFl.RootMarkers)
	}
	for _, name := range []string{"gofmt", "go", "gopls"} {
		if _, ok := goFl.toolByName(name); !ok {
			t.Fatalf("go flavor missing tool %q", name)
		}
	}
	gopls, _ := goFl.toolByName("gopls")
	if gopls.Kind != "lsp" || gopls.Env != "TASTE_GOPLS" || gopls.IssueLanguage != "go" {
		t.Fatalf("unexpected gopls tool def: %#v", gopls)
	}
	strictAction, ok := goFl.actionByName("strict")
	if !ok || !strictAction.RequiresRootMarker || len(strictAction.Steps) != 2 {
		t.Fatalf("unexpected go strict action: %#v", strictAction)
	}

	jsFl, ok := byName["javascript"]
	if !ok {
		t.Fatal("missing javascript flavor")
	}
	wantExts := map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true, ".mts": true, ".cts": true}
	if len(jsFl.Extensions) != len(wantExts) {
		t.Fatalf("unexpected javascript extensions: %#v", jsFl.Extensions)
	}
	for _, e := range jsFl.Extensions {
		if !wantExts[e] {
			t.Fatalf("unexpected extension in javascript flavor: %q", e)
		}
	}
	for _, name := range []string{"npm", "prettier", "eslint", "typescript-language-server"} {
		if _, ok := jsFl.toolByName(name); !ok {
			t.Fatalf("javascript flavor missing tool %q", name)
		}
	}
	tsls, _ := jsFl.toolByName("typescript-language-server")
	if tsls.languageIDFor("main.ts") != "typescript" ||
		tsls.languageIDFor("main.tsx") != "typescriptreact" ||
		tsls.languageIDFor("main.jsx") != "javascriptreact" ||
		tsls.languageIDFor("main.js") != "javascript" ||
		tsls.languageIDFor("main.mjs") != "javascript" {
		t.Fatalf("unexpected typescript-language-server languageIDFor mapping: %#v", tsls)
	}
	strictJS, ok := jsFl.actionByName("strict")
	if !ok || len(strictJS.Steps) != 1 || strictJS.Steps[0].Script != "test" {
		t.Fatalf("unexpected javascript strict action: %#v", strictJS)
	}

	bashFl, ok := byName["bash"]
	if !ok {
		t.Fatal("missing bash flavor")
	}
	if _, ok := bashFl.actionByName("fix"); ok {
		t.Fatalf("bash flavor should have no fix action (bash formatting not configured): %#v", bashFl.Actions.Fix)
	}
	bls, ok := bashFl.toolByName("bash-language-server")
	if !ok || bls.languageIDFor("script.sh") != "shellscript" || bls.IssueLanguage != "bash" {
		t.Fatalf("unexpected bash-language-server tool def: %#v", bls)
	}
}

func TestFlavorMatchesByExtension(t *testing.T) {
	flavors, err := loadFlavors()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		file string
		want string
	}{
		{"main.go", "go"},
		{"main.ts", "javascript"},
		{"main.tsx", "javascript"},
		{"app.jsx", "javascript"},
		{"script.sh", "bash"},
		{"script.zsh", "bash"},
		{"README.md", ""},
	}
	for _, tt := range tests {
		got := ""
		for _, fl := range flavors {
			if fl.matches(tt.file) {
				got = fl.Name
				break
			}
		}
		if got != tt.want {
			t.Errorf("matches(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}
