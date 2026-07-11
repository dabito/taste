package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

//go:embed default_flavors.toml
var defaultFlavorsTOML string

// flavorTool is one resolvable binary a flavor depends on: a plain argv
// tool (gofmt, go, shellcheck) or an LSP server (kind = "lsp").
type flavorTool struct {
	Name             string            `toml:"name"`
	Kind             string            `toml:"kind"`
	Env              string            `toml:"env"`
	Install          string            `toml:"install"`
	LocalNPM         bool              `toml:"local_npm"`
	Args             []string          `toml:"args"`
	IssueLanguage    string            `toml:"issue_language"`
	LanguageID       string            `toml:"language_id"`
	LanguageIDsByExt map[string]string `toml:"language_ids"`
}

func (t flavorTool) languageIDFor(file string) string {
	if t.LanguageIDsByExt != nil {
		if id, ok := t.LanguageIDsByExt[filepath.Ext(file)]; ok {
			return id
		}
	}
	if t.LanguageID != "" {
		return t.LanguageID
	}
	return t.IssueLanguage
}

func (t flavorTool) toolDef() toolDef {
	return toolDef{Name: t.Name, Env: t.Env, Install: t.Install, LocalNPM: t.LocalNPM}
}

// flavorStep is one action of an action (fix/taste/strict): run a fixed
// argv tool, run an LSP tool for diagnostics, or run a repo-declared
// package.json script.
type flavorStep struct {
	Tool                 string   `toml:"tool"`
	Kind                 string   `toml:"kind"`
	Name                 string   `toml:"name"`
	Args                 []string `toml:"args"`
	Script               string   `toml:"script"`
	PerFile              bool     `toml:"per_file"`
	Cwd                  string   `toml:"cwd"`
	Fixable              bool     `toml:"fixable"`
	RequiresConfirmation bool     `toml:"requires_confirmation"`
	ListOutputAsIssues   bool     `toml:"list_output_as_issues"`
}

func (s flavorStep) displayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Tool
}

type flavorAction struct {
	RequiresRootMarker bool         `toml:"requires_root_marker"`
	Steps              []flavorStep `toml:"steps"`
}

type flavorActions struct {
	Fix    flavorAction `toml:"fix"`
	Taste  flavorAction `toml:"taste"`
	Strict flavorAction `toml:"strict"`
}

type flavorDef struct {
	Name        string        `toml:"name"`
	Extensions  []string      `toml:"extensions"`
	RootMarkers []string      `toml:"root_markers"`
	Tools       []flavorTool  `toml:"tool"`
	Actions     flavorActions `toml:"actions"`
}

func (f flavorDef) toolByName(name string) (flavorTool, bool) {
	for _, t := range f.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return flavorTool{}, false
}

func (f flavorDef) actionByName(name string) (flavorAction, bool) {
	switch name {
	case "fix":
		return f.Actions.Fix, len(f.Actions.Fix.Steps) > 0
	case "taste":
		return f.Actions.Taste, len(f.Actions.Taste.Steps) > 0
	case "strict":
		return f.Actions.Strict, len(f.Actions.Strict.Steps) > 0
	default:
		return flavorAction{}, false
	}
}

func (f flavorDef) matches(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range f.Extensions {
		if e == ext {
			return true
		}
	}
	return false
}

type flavorConfig struct {
	Flavor []flavorDef `toml:"flavor"`
}

// loadFlavors resolves the flavor registry: an embedded built-in default,
// overridden whole-flavor-by-name by a project-local .taste/flavors.toml
// (discovered by walking up from cwd) and/or a user-level
// $XDG_CONFIG_HOME/taste/flavors.toml (or ~/.config/taste/flavors.toml).
// The built-in default always loads standalone -- a fresh install must
// never require a config file on disk to diagnose go/js/bash.
func loadFlavors() ([]flavorDef, error) {
	base, err := parseFlavorTOML(defaultFlavorsTOML)
	if err != nil {
		return nil, fmt.Errorf("embedded default_flavors.toml is invalid: %w", err)
	}
	byName := map[string]flavorDef{}
	order := make([]string, 0, len(base))
	for _, fl := range base {
		byName[fl.Name] = fl
		order = append(order, fl.Name)
	}

	applyOverride := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		overrides, err := parseFlavorTOML(string(data))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, fl := range overrides {
			if _, exists := byName[fl.Name]; !exists {
				order = append(order, fl.Name)
			}
			byName[fl.Name] = fl
		}
		return nil
	}

	if projectPath, ok := findProjectFlavorsFile(); ok {
		if err := applyOverride(projectPath); err != nil {
			return nil, err
		}
	}
	if userPath, ok := userFlavorsPath(); ok {
		if err := applyOverride(userPath); err != nil {
			return nil, err
		}
	}

	flavors := make([]flavorDef, 0, len(order))
	for _, name := range order {
		flavors = append(flavors, byName[name])
	}
	return flavors, nil
}

func parseFlavorTOML(data string) ([]flavorDef, error) {
	var cfg flavorConfig
	if _, err := toml.Decode(data, &cfg); err != nil {
		return nil, err
	}
	return cfg.Flavor, nil
}

func findProjectFlavorsFile() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, ".taste", "flavors.toml")
		if fileExists(candidate) {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func userFlavorsPath() (string, bool) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		configHome = filepath.Join(home, ".config")
	}
	path := filepath.Join(configHome, "taste", "flavors.toml")
	return path, fileExists(path)
}
