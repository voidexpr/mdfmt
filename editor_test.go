package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testEditorHost = "127.0.0.1:8642"

func attachTestEditor(t *testing.T, server *markdownServer, commandLog io.Writer, args ...string) *editorLauncher {
	t.Helper()
	printfPath, err := exec.LookPath("printf")
	if err != nil {
		t.Skipf("printf is unavailable: %v", err)
	}
	editor, err := newEditorLauncher(serveConfig{
		editCommand: printfPath,
		editArgs:    args,
	}, server.root, commandLog, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	editor.allowedHost = testEditorHost
	server.editor = editor
	return editor
}

func editRequest(t *testing.T, server http.Handler, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://"+testEditorHost+editEndpoint, strings.NewReader(values.Encode()))
	req.Host = testEditorHost
	req.Header.Set("Origin", "http://"+testEditorHost)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	return response
}

func TestEditForksCommandWithResolvedMarkdownPath(t *testing.T) {
	root := t.TempDir()
	filename := writeTestFile(t, root, "plans/feature with space.md", "# Feature\n")
	server := testServer(t, root)
	var commandLog, childOutput bytes.Buffer
	editor := attachTestEditor(t, server, &commandLog, "%s")
	editor.childStdout = &childOutput
	waitDone := make(chan error, 1)
	editor.afterWaitForTest = waitDone

	response := editRequest(t, server, url.Values{
		"token":  {editor.token},
		"path":   {"/plans/feature%20with%20space.md"},
		"return": {"/plans/"},
	})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "/plans/" {
		t.Errorf("Location = %q, want /plans/", location)
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("printf command: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for printf command")
	}

	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got := childOutput.String(); got != resolved {
		t.Errorf("printf output = %q, want resolved path %q", got, resolved)
	}
	wantCommand := formatCommand([]string{editor.executable, "%s", resolved}) + "\n"
	if got := commandLog.String(); got != wantCommand {
		t.Errorf("command log = %q, want %q", got, wantCommand)
	}
}

func TestEditUsesFinalSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	realPath := writeTestFile(t, root, "real.markdown", "# Real\n")
	aliasPath := filepath.Join(root, "alias.md")
	if err := os.Symlink(realPath, aliasPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	server := testServer(t, root)
	var commandLog bytes.Buffer
	editor := attachTestEditor(t, server, &commandLog, "%s")
	waitDone := make(chan error, 1)
	editor.afterWaitForTest = waitDone

	response := editRequest(t, server, url.Values{
		"token": {editor.token},
		"path":  {"/alias.md"},
	})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", response.Code, response.Body.String())
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for printf command")
	}
	resolvedReal, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commandLog.String(), strconv.Quote(resolvedReal)) {
		t.Errorf("command did not use resolved target %q: %s", resolvedReal, commandLog.String())
	}
	if strings.Contains(commandLog.String(), strconv.Quote(aliasPath)) {
		t.Errorf("command used unresolved symlink %q: %s", aliasPath, commandLog.String())
	}
}

func TestEditRejectsInvalidAndEscapingPaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "docs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "guide.md", "# Guide\n")
	writeTestFile(t, root, "notes.txt", "not Markdown")
	writeTestFile(t, root, ".hidden.md", "# Hidden\n")
	outside := writeTestFile(t, parent, "outside.md", "# Outside\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "notes.txt"), filepath.Join(root, "disguised.md")); err != nil {
		t.Fatal(err)
	}
	server := testServer(t, root)
	var commandLog bytes.Buffer
	editor := attachTestEditor(t, server, &commandLog, "%s")

	tests := []struct {
		name   string
		path   string
		status int
	}{
		{"non markdown", "/notes.txt", http.StatusNotFound},
		{"directory", "/", http.StatusNotFound},
		{"hidden", "/.hidden.md", http.StatusNotFound},
		{"plain traversal", "/../outside.md", http.StatusForbidden},
		{"encoded traversal", "/%2e%2e/outside.md", http.StatusForbidden},
		{"outside symlink", "/escape.md", http.StatusForbidden},
		{"markdown alias to non markdown", "/disguised.md", http.StatusNotFound},
		{"machine absolute path", "/etc/passwd", http.StatusNotFound},
		{"private key style path", "/.ssh/mykey.priv", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := editRequest(t, server, url.Values{
				"token": {editor.token},
				"path":  {tt.path},
			})
			if response.Code != tt.status {
				t.Errorf("status = %d, want %d; body=%s", response.Code, tt.status, response.Body.String())
			}
		})
	}
	lines := strings.Split(strings.TrimSpace(commandLog.String()), "\n")
	if len(lines) != len(tests) {
		t.Fatalf("audit line count = %d, want %d:\n%s", len(lines), len(tests), commandLog.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "EDIT-ERROR ") {
			t.Errorf("rejected edit was not logged as an error: %q", line)
		}
	}
	for _, want := range []string{
		`EDIT-ERROR "/notes.txt" "not found"`,
		`EDIT-ERROR "/../outside.md" "path traversal rejected"`,
		`EDIT-ERROR "/escape.md" "symlink escapes root"`,
	} {
		if !strings.Contains(commandLog.String(), want+"\n") {
			t.Errorf("audit log does not contain exact entry %q:\n%s", want, commandLog.String())
		}
	}
}

func TestEditEndpointRequiresPostSameOriginAndToken(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "guide.md", "# Guide\n")
	server := testServer(t, root)
	var commandLog bytes.Buffer
	editor := attachTestEditor(t, server, &commandLog, "%s")

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+testEditorHost+editEndpoint, nil)
		req.Host = testEditorHost
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", response.Code)
		}
	})

	for _, tt := range []struct {
		name   string
		host   string
		origin string
		token  string
	}{
		{"wrong host", "attacker.invalid:8642", "http://" + testEditorHost, editor.token},
		{"wrong host port", "127.0.0.1:9999", "http://" + testEditorHost, editor.token},
		{"wrong origin", testEditorHost, "http://attacker.invalid:8642", editor.token},
		{"missing token", testEditorHost, "http://" + testEditorHost, ""},
		{"wrong token", testEditorHost, "http://" + testEditorHost, "wrong"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := url.Values{"path": {"/guide.md"}, "token": {tt.token}}
			req := httptest.NewRequest(http.MethodPost, "http://"+testEditorHost+editEndpoint, strings.NewReader(values.Encode()))
			req.Host = tt.host
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", response.Code)
			}
		})
	}
	lines := strings.Split(strings.TrimSpace(commandLog.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("audit line count = %d, want 6:\n%s", len(lines), commandLog.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "EDIT-ERROR ") {
			t.Errorf("unauthorized request was not logged as an error: %q", line)
		}
	}
}

func TestEditAcceptsEquivalentLoopbackHostsAndMissingOrigin(t *testing.T) {
	for _, tt := range []struct {
		name   string
		host   string
		origin string
	}{
		{"localhost alias", "localhost:8642", "http://localhost:8642"},
		{"missing origin", testEditorHost, ""},
		{"null origin", testEditorHost, "null"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			filename := writeTestFile(t, root, "guide.md", "# Guide\n")
			server := testServer(t, root)
			var commandLog bytes.Buffer
			editor := attachTestEditor(t, server, &commandLog, "%s")
			waitDone := make(chan error, 1)
			editor.afterWaitForTest = waitDone

			values := url.Values{"path": {"/guide.md"}, "token": {editor.token}}
			req := httptest.NewRequest(http.MethodPost, "http://"+tt.host+editEndpoint, strings.NewReader(values.Encode()))
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303; body=%s", response.Code, response.Body.String())
			}
			select {
			case err := <-waitDone:
				if err != nil {
					t.Fatalf("printf command: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for printf command")
			}
			resolved, err := filepath.EvalSymlinks(filename)
			if err != nil {
				t.Fatal(err)
			}
			want := formatCommand([]string{editor.executable, "%s", resolved}) + "\n"
			if got := commandLog.String(); got != want {
				t.Errorf("command log = %q, want %q", got, want)
			}
		})
	}
}

func TestEditEndpointIsAbsentWhenDisabled(t *testing.T) {
	server := testServer(t, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, editEndpoint, strings.NewReader("path=/guide.md"))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestEditControlsAppearOnDirectoryAndDocumentPages(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "guide.md", "# Guide\n")
	writeTestFile(t, root, "other.markdown", "# Other\n")
	writeTestFile(t, root, "notes.txt", "not shown")
	server := testServer(t, root)
	editor := attachTestEditor(t, server, io.Discard, "%s")

	directory := request(t, server, "/")
	if directory.Code != http.StatusOK {
		t.Fatalf("directory status = %d", directory.Code)
	}
	directoryBody := directory.Body.String()
	if count := strings.Count(directoryBody, `action="/.mdfmt/edit"`); count != 2 {
		t.Errorf("directory edit-form count = %d, want 2:\n%s", count, directoryBody)
	}
	for _, want := range []string{
		`name="path" value="/guide.md"`,
		`name="path" value="/other.markdown"`,
		`name="return" value="/"`,
		`class="directory-table can-edit"`,
	} {
		if !strings.Contains(directoryBody, want) {
			t.Errorf("directory page does not contain %q", want)
		}
	}
	if strings.Contains(directoryBody, "notes.txt") {
		t.Error("directory page exposes a non-Markdown file")
	}

	document := request(t, server, "/guide.md")
	if document.Code != http.StatusOK {
		t.Fatalf("document status = %d", document.Code)
	}
	documentBody := document.Body.String()
	for _, want := range []string{
		`class="page-toolbar"`,
		`action="/.mdfmt/edit"`,
		`name="token" value="` + editor.token + `"`,
		`name="path" value="/guide.md"`,
		`name="return" value="/guide.md"`,
	} {
		if !strings.Contains(documentBody, want) {
			t.Errorf("document page does not contain %q", want)
		}
	}
	if count := strings.Count(documentBody, `action="/.mdfmt/edit"`); count != 1 {
		t.Errorf("document edit-form count = %d, want 1", count)
	}
	if csp := document.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self'") {
		t.Errorf("editing CSP does not permit same-origin form: %q", csp)
	}
}

func TestEditorFlagValidation(t *testing.T) {
	cfg, err := parseServeFlags([]string{
		"--edit-command", "printf",
		"--edit-arg=%s",
		"./plans",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.editCommand != "printf" || len(cfg.editArgs) != 1 || cfg.editArgs[0] != "%s" {
		t.Errorf("editor flags = %#v", cfg)
	}

	for _, tt := range []struct {
		name string
		args []string
	}{
		{"mutually exclusive presets", []string{"--edit-sublime", "--edit-vscode"}},
		{"preset and command", []string{"--edit-sublime", "--edit-command", "printf"}},
		{"argument without editor", []string{"--edit-arg=%s"}},
		{"non-loopback command", []string{"--bind", "0.0.0.0", "--edit-command", "printf"}},
		{"non-loopback preset", []string{"--bind", "192.0.2.1", "--edit-sublime"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseServeFlags(tt.args, io.Discard); err == nil {
				t.Fatal("invalid editor flags were accepted")
			}
		})
	}

	for _, args := range [][]string{{"--edit-sublime"}, {"--edit-vscode"}, {"--bind", "::1", "--edit-vscode"}} {
		if _, err := parseServeFlags(args, io.Discard); err != nil {
			t.Errorf("valid preset %v: %v", args, err)
		}
	}
}

func TestEditorCommandMustExistAndBeExecutable(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-editor")
	_, err := newEditorLauncher(
		serveConfig{editCommand: missing},
		root,
		io.Discard,
		log.New(io.Discard, "", 0),
	)
	if err == nil {
		t.Fatal("missing editor command was accepted")
	}
	for _, want := range []string{
		"editor command not found or not executable",
		strconv.Quote(missing),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	if runtime.GOOS == "windows" {
		return
	}
	notExecutable := writeTestFile(t, root, "not-executable", "#!/bin/sh\n")
	if err := os.Chmod(notExecutable, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = newEditorLauncher(
		serveConfig{editCommand: notExecutable},
		root,
		io.Discard,
		log.New(io.Discard, "", 0),
	)
	if err == nil {
		t.Fatal("non-executable editor command was accepted")
	}
	for _, want := range []string{
		"editor command not found or not executable",
		strconv.Quote(notExecutable),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
