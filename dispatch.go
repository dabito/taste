package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// classifyByFlavor groups paths by the first matching flavor's name, in
// flavor-registry order. A path matching no flavor is silently dropped
// (matches prior classifyFiles behavior for unsupported extensions).
func classifyByFlavor(paths []string, flavors []flavorDef) map[string][]string {
	groups := map[string][]string{}
	for _, p := range paths {
		for _, fl := range flavors {
			if fl.matches(p) {
				groups[fl.Name] = append(groups[fl.Name], p)
				break
			}
		}
	}
	return groups
}

// runFlavorAction runs one action (fix/taste/strict) of one flavor against
// the given files, dispatching each configured step generically by kind.
// This replaces the hand-written runGo/runJS/runBash.
func runFlavorAction(res *result, fl flavorDef, files []string, actionName string, allowScripts bool) {
	action, ok := fl.actionByName(actionName)
	if !ok {
		if actionName == "fix" {
			res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: fl.Name + " formatting not configured"})
		}
		return
	}

	root := findWorkspaceRoot(files)

	if action.RequiresRootMarker && !hasAnyRootMarker(root, fl.RootMarkers) {
		res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: actionName + " skipped: no " + strings.Join(fl.RootMarkers, "/") + " found in workspace"})
		return
	}

	var npmScripts map[string]bool
	npmChecked := false
	npmOK := true

	for _, step := range action.Steps {
		switch step.Kind {
		case "lsp":
			runFlavorLSPStep(res, fl, root, files, step)
		case "npm_script":
			if !npmChecked {
				npmChecked = true
				npmScripts = packageScripts(root)
				if len(npmScripts) == 0 {
					res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: "package.json scripts not found"})
					npmOK = false
				} else if _, ok := resolveToolInDir(toolDefByName("npm"), root); !ok {
					res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: "npm not found; override with TASTE_NPM"})
					npmOK = false
				}
			}
			if !npmOK {
				continue
			}
			runFlavorNPMScriptStep(res, fl, root, npmScripts, step, allowScripts)
		default:
			runFlavorArgvStep(res, fl, root, files, step)
		}
	}
}

func hasAnyRootMarker(root string, markers []string) bool {
	if len(markers) == 0 {
		return true
	}
	for _, m := range markers {
		if fileExists(filepath.Join(root, m)) {
			return true
		}
	}
	return false
}

func runFlavorLSPStep(res *result, fl flavorDef, root string, files []string, step flavorStep) {
	tool, ok := fl.toolByName(step.Tool)
	if !ok {
		return
	}

	var initOptions map[string]any
	if tool.Name == "typescript-language-server" {
		if _, ok := resolveToolInDir(toolDefByName(tool.Name), root); ok {
			if tsserverPath, ok := resolveTsserverPath(root); ok {
				initOptions = map[string]any{"tsserver": map[string]any{"path": tsserverPath}}
			}
		}
	}

	issues, summary, err := runLSPDiagnostics(lspRunConfig{
		ToolName:       tool.Name,
		ToolArgs:       tool.Args,
		InstallHint:    tool.Install,
		Root:           root,
		Files:          files,
		IssueLanguage:  tool.IssueLanguage,
		LanguageIDFunc: tool.languageIDFor,
		InitOptions:    initOptions,
	})
	if err != nil {
		reportToolFailure(res, fl.Name, tool.Name, err)
		return
	}
	status := "pass"
	if len(issues) > 0 {
		status = "fail"
	}
	if summary == "" {
		summary = fmt.Sprintf("%d diagnostics", len(issues))
	}
	res.Commands = append(res.Commands, commandItem{Name: step.displayName(), Status: status, Summary: summary})
	res.Issues = append(res.Issues, issues...)
}

func runFlavorNPMScriptStep(res *result, fl flavorDef, root string, scripts map[string]bool, step flavorStep, allowScripts bool) {
	name := "npm run " + step.Script
	if !scripts[step.Script] {
		res.Commands = append(res.Commands, commandItem{Name: name, Status: "skip"})
		res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: "npm script missing: " + step.Script})
		return
	}
	if step.RequiresConfirmation && !gateRepoScript(res, fl.Name, name, allowScripts) {
		return
	}
	status, summary := runExternalInDir(root, "npm", "run", step.Script)
	res.Commands = append(res.Commands, commandItem{Name: name, Status: status, Summary: summary})
	if status == "fail" {
		res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", Code: name, Message: summary})
	}
}

// runFlavorArgvStep runs a fixed, known-safe binary (gofmt, go, bash,
// shellcheck) with templated args: "{files}" expands to the whole batch in
// one invocation (per_file = false), "{file}" loops once per file
// (per_file = true), producing one commandItem per invocation.
func runFlavorArgvStep(res *result, fl flavorDef, root string, files []string, step flavorStep) {
	dir := "."
	if step.Cwd == "root" {
		dir = root
	}
	if step.Optional {
		if _, ok := resolveToolInDir(toolDefByName(step.Tool), dir); !ok {
			res.Warnings = append(res.Warnings, warningItem{Language: fl.Name, Message: fmt.Sprintf("%s not found; override with %s", step.Tool, toolDefByName(step.Tool).Env)})
			return
		}
	}
	if step.PerFile {
		for _, f := range files {
			args := expandArgs(step.Args, nil, f)
			name := step.displayName() + " " + f
			status, summary := runExternalInDir(dir, step.Tool, args...)
			res.Commands = append(res.Commands, commandItem{Name: name, Status: status, Summary: summary})
			if status == "fail" {
				res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", File: f, Code: step.displayName(), Message: summary})
			}
		}
		return
	}

	args := expandArgs(step.Args, files, "")

	if step.Fixable && step.Kind == "argv" && !step.ListOutputAsIssues {
		// A fix-action step (e.g. gofmt -l -w): the tool's own output lists
		// which files it actually changed, so we report that count rather
		// than assuming every file we handed it needed changing.
		status, raw := runExternalInDirRaw(dir, step.Tool, args...)
		summary := summarizeOutput([]byte(raw))
		res.Commands = append(res.Commands, commandItem{Name: step.displayName(), Status: status, Summary: summary})
		if status == "pass" {
			res.Fixed = append(res.Fixed, fixedItem{Language: fl.Name, Kind: "format", Files: len(nonEmptyLines(raw))})
			return
		}
		res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", Code: step.displayName(), Message: summary})
		return
	}

	status, summary := runExternalInDir(dir, step.Tool, args...)
	res.Commands = append(res.Commands, commandItem{Name: step.displayName(), Status: status, Summary: summary})

	if step.ListOutputAsIssues {
		// e.g. gofmt -l: exit 0 with a list of unformatted files on stdout
		// is itself the failure signal, not a clean pass.
		if status == "pass" && strings.TrimSpace(summary) != "" {
			for _, f := range strings.Fields(summary) {
				res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", File: f, Code: step.displayName(), Message: "file is not formatted"})
			}
		} else if status == "fail" {
			res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", Code: step.displayName(), Message: summary})
		}
		return
	}

	if status == "fail" {
		res.Issues = append(res.Issues, issueItem{Language: fl.Name, Severity: "error", Code: step.displayName(), Message: summary})
	}
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func expandArgs(template []string, files []string, file string) []string {
	args := make([]string, 0, len(template)+len(files))
	for _, a := range template {
		switch a {
		case "{files}":
			args = append(args, files...)
		case "{file}":
			args = append(args, file)
		default:
			args = append(args, a)
		}
	}
	return args
}
