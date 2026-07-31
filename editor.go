package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const editEndpoint = assetPrefix + "edit"

type editorLauncher struct {
	executable       string
	args             []string
	root             string
	token            string
	allowedHost      string
	commandOutput    io.Writer
	childStdout      io.Writer
	childStderr      io.Writer
	logger           *log.Logger
	afterWaitForTest chan<- error
	outputMu         sync.Mutex
}

type editAction struct {
	Action    template.URL
	Path      string
	ReturnURL string
	Token     string
	Label     string
}

func newEditorLauncher(cfg serveConfig, root string, stdout io.Writer, logger *log.Logger) (*editorLauncher, error) {
	if cfg.editCommand == "" && !cfg.editSublime && !cfg.editVSCode {
		return nil, nil
	}

	candidates := editorCommandCandidates(cfg)
	var executable string
	for _, candidate := range candidates {
		resolved, err := resolveEditorExecutable(candidate)
		if err == nil {
			executable = resolved
			break
		}
	}
	if executable == "" {
		quoted := make([]string, len(candidates))
		for i, candidate := range candidates {
			quoted[i] = strconv.Quote(candidate)
		}
		return nil, fmt.Errorf(
			"editor command not found or not executable; tried %s",
			strings.Join(quoted, ", "),
		)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("create editor request token: %w", err)
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &editorLauncher{
		executable:    executable,
		args:          append([]string(nil), cfg.editArgs...),
		root:          root,
		token:         base64.RawURLEncoding.EncodeToString(tokenBytes),
		commandOutput: stdout,
		childStdout:   io.Discard,
		childStderr:   io.Discard,
		logger:        logger,
	}, nil
}

func editorCommandCandidates(cfg serveConfig) []string {
	switch {
	case cfg.editSublime:
		candidates := []string{"subl"}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, "/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl")
		}
		return candidates
	case cfg.editVSCode:
		candidates := []string{"code"}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code")
		}
		return candidates
	default:
		return []string{cfg.editCommand}
	}
}

func resolveEditorExecutable(command string) (string, error) {
	if command == "" {
		return "", errors.New("editor command is empty")
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("%q: %w", command, err)
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func (s *markdownServer) editAction(components, pageDirectory, returnComponents []string, returnDirectory bool) *editAction {
	if s.editor == nil {
		return nil
	}
	label := "Markdown file"
	if len(components) > 0 {
		label = components[len(components)-1]
	}
	return &editAction{
		Action:    template.URL(s.pageURL(pageDirectory, []string{".mdfmt", "edit"}, false)),
		Path:      escapedURL(components, false),
		ReturnURL: escapedURL(returnComponents, returnDirectory),
		Token:     s.editor.token,
		Label:     label,
	}
}

func (s *markdownServer) serveEdit(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.logger.Printf("EDIT-ERROR %q %q", "", "editor integration is disabled")
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.rejectEdit(w, http.StatusMethodNotAllowed, "", errors.New("edit endpoint requires POST"))
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		s.rejectEdit(w, http.StatusUnsupportedMediaType, "", errors.New("edit request must use application/x-www-form-urlencoded"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		s.rejectEdit(w, http.StatusBadRequest, "", fmt.Errorf("parse edit request: %w", err))
		return
	}
	requestPath := r.PostForm.Get("path")
	if err := s.editor.validateRequestSource(r); err != nil {
		s.rejectEdit(w, http.StatusForbidden, requestPath, err)
		return
	}
	providedToken := r.PostForm.Get("token")
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(s.editor.token)) != 1 {
		s.rejectEdit(w, http.StatusForbidden, requestPath, errors.New("invalid editor request token"))
		return
	}

	resolved, err := s.resolveEditablePath(requestPath)
	if err != nil {
		s.rejectEdit(w, routeStatus(err), requestPath, err)
		return
	}
	if err := s.editor.launch(resolved); err != nil {
		s.rejectEdit(w, http.StatusInternalServerError, resolved, fmt.Errorf("launch editor: %w", err))
		return
	}

	returnURL := safeLocalReturnURL(r.PostForm.Get("return"))
	if returnURL == "" {
		returnURL = "/"
	}
	if s.pathToken != "" {
		returnURL = "/" + s.pathToken + returnURL
	}
	http.Redirect(w, r, returnURL, http.StatusSeeOther)
}

func (s *markdownServer) rejectEdit(w http.ResponseWriter, status int, path string, err error) {
	s.editor.logFailure(path, err)
	http.Error(w, strings.ToLower(http.StatusText(status)), status)
}

func routeStatus(err error) int {
	var routeErr *routeError
	if errors.As(err, &routeErr) {
		return routeErr.status
	}
	return http.StatusInternalServerError
}

func (l *editorLauncher) validateRequestSource(r *http.Request) error {
	expectedPort, err := loopbackPort(l.allowedHost)
	if err != nil {
		return fmt.Errorf("invalid configured editor host %q: %w", l.allowedHost, err)
	}
	requestPort, err := loopbackPort(r.Host)
	if err != nil {
		return fmt.Errorf("request host %q is not allowed: %w", r.Host, err)
	}
	if requestPort != expectedPort {
		return fmt.Errorf("request host %q uses port %q, expected %q", r.Host, requestPort, expectedPort)
	}

	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return nil
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme != "http" || originURL.User != nil || originURL.Path != "" {
		return fmt.Errorf("request origin %q is not an allowed loopback origin", origin)
	}
	originPort, err := loopbackPort(originURL.Host)
	if err != nil || originPort != expectedPort {
		return fmt.Errorf("request origin %q is not an allowed loopback origin on port %q", origin, expectedPort)
	}
	return nil
}

func loopbackPort(hostPort string) (string, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", errors.New("host must include a port")
	}
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("host is not a loopback address")
		}
	}
	if port == "" {
		return "", errors.New("port is empty")
	}
	return port, nil
}

func (s *markdownServer) resolveEditablePath(rawPath string) (string, error) {
	target, err := url.ParseRequestURI(rawPath)
	if err != nil || target.Scheme != "" || target.Host != "" || target.RawQuery != "" || target.Fragment != "" {
		return "", &routeError{status: http.StatusBadRequest, err: errors.New("invalid edit path")}
	}
	components, err := requestComponents(target)
	if err != nil {
		return "", err
	}
	if len(components) == 0 || strings.HasSuffix(target.Path, "/") || !isMarkdownName(components[len(components)-1]) {
		return "", &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	}
	resolved, info, err := s.resolve(components)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(resolved) ||
		!withinRoot(s.root, resolved) ||
		hasHiddenComponent(s.root, resolved) ||
		!info.Mode().IsRegular() ||
		!isMarkdownName(info.Name()) {
		return "", &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	}
	return resolved, nil
}

func safeLocalReturnURL(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") {
		return ""
	}
	target, err := url.ParseRequestURI(raw)
	if err != nil || target.Scheme != "" || target.Host != "" || target.Fragment != "" {
		return ""
	}
	return target.RequestURI()
}

func (l *editorLauncher) launch(path string) error {
	resolved, info, err := resolveExisting(path)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(resolved) ||
		!withinRoot(l.root, resolved) ||
		hasHiddenComponent(l.root, resolved) ||
		!info.Mode().IsRegular() ||
		!isMarkdownName(info.Name()) {
		return errors.New("editor path is not an allowed Markdown file")
	}

	argv := make([]string, 0, len(l.args)+2)
	argv = append(argv, l.executable)
	argv = append(argv, l.args...)
	argv = append(argv, resolved)

	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = l.root
	command.Stdout = l.childStdout
	command.Stderr = l.childStderr
	if err := command.Start(); err != nil {
		return err
	}

	l.outputMu.Lock()
	fmt.Fprintln(l.commandOutput, formatCommand(argv))
	l.outputMu.Unlock()

	go func() {
		err := command.Wait()
		if err != nil {
			l.logger.Printf("editor command exited: %v", err)
		}
		if l.afterWaitForTest != nil {
			l.afterWaitForTest <- err
		}
	}()
	return nil
}

func (l *editorLauncher) logFailure(path string, err error) {
	l.outputMu.Lock()
	fmt.Fprintf(l.commandOutput, "EDIT-ERROR %s %s\n", strconv.Quote(path), strconv.Quote(err.Error()))
	l.outputMu.Unlock()
}

func formatCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, argument := range argv {
		quoted[i] = strconv.Quote(argument)
	}
	return strings.Join(quoted, " ")
}
