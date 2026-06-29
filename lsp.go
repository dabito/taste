package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspDiagnostic struct {
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     any    `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

type lspRunConfig struct {
	ToolName       string
	ToolArgs       []string
	InstallHint    string
	Root           string
	Files          []string
	IssueLanguage  string
	LanguageIDFunc func(string) string
}

func runGoplsDiagnostics(root string, files []string) ([]issueItem, string, error) {
	return runLSPDiagnostics(lspRunConfig{
		ToolName:      "gopls",
		InstallHint:   "go install golang.org/x/tools/gopls@latest",
		Root:          root,
		Files:         files,
		IssueLanguage: "go",
		LanguageIDFunc: func(string) string {
			return "go"
		},
	})
}

func runTypeScriptDiagnostics(root string, files []string) ([]issueItem, string, error) {
	return runLSPDiagnostics(lspRunConfig{
		ToolName:      "typescript-language-server",
		ToolArgs:      []string{"--stdio"},
		InstallHint:   "npm install -D typescript-language-server typescript",
		Root:          root,
		Files:         files,
		IssueLanguage: "javascript",
		LanguageIDFunc: func(file string) string {
			switch filepath.Ext(file) {
			case ".ts", ".mts", ".cts":
				return "typescript"
			case ".tsx":
				return "typescriptreact"
			case ".jsx":
				return "javascriptreact"
			default:
				return "javascript"
			}
		},
	})
}

func runLSPDiagnostics(config lspRunConfig) ([]issueItem, string, error) {
	absRoot, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, "", err
	}
	path, ok := resolveToolInDir(toolDefByName(config.ToolName), absRoot)
	if !ok {
		return nil, "", fmt.Errorf("%s not found; install with %s or set %s", config.ToolName, config.InstallHint, toolDefByName(config.ToolName).Env)
	}
	absFiles := make([]string, 0, len(config.Files))
	for _, file := range config.Files {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, "", err
		}
		absFiles = append(absFiles, abs)
	}

	cmd := exec.Command(path, config.ToolArgs...)
	cmd.Dir = absRoot
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}

	messages := make(chan rpcMessage, 64)
	readErrs := make(chan error, 1)
	go func() {
		readErrs <- readRPCMessages(stdout, messages)
	}()
	writer := &rpcWriter{w: stdin}

	rootURI := fileURI(absRoot)
	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": true},
			},
		},
		"workspaceFolders": []map[string]string{{"uri": rootURI, "name": filepath.Base(absRoot)}},
	}
	if err := writer.request(1, "initialize", initParams); err != nil {
		cleanupLSP(cmd, stdin)
		return nil, "", err
	}
	if err := waitForResponse(messages, 1, 10*time.Second); err != nil {
		cleanupLSP(cmd, stdin)
		return nil, strings.TrimSpace(stderr.String()), err
	}
	if err := writer.notify("initialized", map[string]any{}); err != nil {
		cleanupLSP(cmd, stdin)
		return nil, "", err
	}

	wanted := map[string]string{}
	for _, file := range absFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			cleanupLSP(cmd, stdin)
			return nil, "", err
		}
		uri := fileURI(file)
		wanted[uri] = file
		params := map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": config.LanguageIDFunc(file),
				"version":    1,
				"text":       string(data),
			},
		}
		if err := writer.notify("textDocument/didOpen", params); err != nil {
			cleanupLSP(cmd, stdin)
			return nil, "", err
		}
	}

	diagByURI := map[string][]lspDiagnostic{}
	deadline := time.After(3 * time.Second)
	for len(diagByURI) < len(wanted) {
		select {
		case msg := <-messages:
			if msg.Method != "textDocument/publishDiagnostics" {
				continue
			}
			var params publishDiagnosticsParams
			if json.Unmarshal(msg.Params, &params) != nil {
				continue
			}
			if _, ok := wanted[params.URI]; ok {
				diagByURI[params.URI] = params.Diagnostics
			}
		case <-deadline:
			goto done
		}
	}

done:
	_ = writer.request(2, "shutdown", nil)
	_ = waitForResponse(messages, 2, time.Second)
	_ = writer.notify("exit", nil)
	_ = stdin.Close()
	_ = cmd.Wait()
	select {
	case <-readErrs:
	default:
	}

	issues := make([]issueItem, 0)
	for uri, diagnostics := range diagByURI {
		file := wanted[uri]
		if cwd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
				file = rel
			}
		}
		for _, diag := range diagnostics {
			issues = append(issues, issueItem{
				Language: config.IssueLanguage,
				Severity: lspSeverity(diag.Severity),
				File:     file,
				Line:     diag.Range.Start.Line + 1,
				Code:     lspCode(diag),
				Message:  diag.Message,
			})
		}
	}
	return issues, strings.TrimSpace(stderr.String()), nil
}

type rpcWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *rpcWriter) request(id int, method string, params any) error {
	return w.write(rpcMessage{JSONRPC: "2.0", ID: id, Method: method, Params: mustRaw(params)})
}

func (w *rpcWriter) notify(method string, params any) error {
	return w.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustRaw(params)})
}

func (w *rpcWriter) write(msg rpcMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = fmt.Fprintf(w.w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func mustRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	body, _ := json.Marshal(v)
	return body
}

func readRPCMessages(r io.Reader, out chan<- rpcMessage) error {
	br := bufio.NewReader(r)
	for {
		length := -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return err
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				value := strings.TrimSpace(line[len("content-length:"):])
				n, err := strconv.Atoi(value)
				if err != nil {
					return err
				}
				length = n
			}
		}
		if length < 0 {
			return fmt.Errorf("missing Content-Length")
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(br, body); err != nil {
			return err
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			return err
		}
		out <- msg
	}
}

func waitForResponse(messages <-chan rpcMessage, id int, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-messages:
			if !sameID(msg.ID, id) {
				continue
			}
			if msg.Error != nil {
				return fmt.Errorf("lsp error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			return nil
		case <-deadline:
			return fmt.Errorf("timed out waiting for LSP response %d", id)
		}
	}
}

func sameID(id any, want int) bool {
	switch v := id.(type) {
	case float64:
		return int(v) == want
	case int:
		return v == want
	case string:
		return v == strconv.Itoa(want)
	default:
		return false
	}
}

func cleanupLSP(cmd *exec.Cmd, stdin io.Closer) {
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

func findWorkspaceRoot(paths []string) string {
	start := "."
	if len(paths) > 0 {
		start = paths[0]
		if info, err := os.Stat(start); err == nil && !info.IsDir() {
			start = filepath.Dir(start)
		}
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "."
	}
	for {
		if fileExists(filepath.Join(abs, "go.mod")) || fileExists(filepath.Join(abs, "package.json")) || fileExists(filepath.Join(abs, "tsconfig.json")) || fileExists(filepath.Join(abs, ".git")) {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func lspSeverity(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "warning"
	}
}

func lspCode(diag lspDiagnostic) string {
	if diag.Code == nil {
		return diag.Source
	}
	code := fmt.Sprint(diag.Code)
	if diag.Source != "" {
		return diag.Source + ":" + code
	}
	return code
}
