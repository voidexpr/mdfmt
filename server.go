package main

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/voidexpr/mdfmt/internal/mdhighlight"
)

const assetPrefix = "/.mdfmt/"

//go:embed assets/* templates/*
var embeddedFiles embed.FS

// The templates and assets are embedded at build time, so both commands share a
// single parsed template set and one copy of each asset.
var (
	pageTemplates       = template.Must(template.New("mdfmt").ParseFS(embeddedFiles, "templates/*.html"))
	stylesheetAsset     = joinAssets("assets/style.css", "assets/syntax.css")
	scriptAsset         = mustReadAsset("assets/app.js")
	faviconSVGAsset     = mustReadAsset("assets/favicon.svg")
	faviconICOAsset     = mustReadAsset("assets/favicon.ico")
	favicon16Asset      = mustReadAsset("assets/favicon-16.png")
	favicon32Asset      = mustReadAsset("assets/favicon-32.png")
	favicon48Asset      = mustReadAsset("assets/favicon-48.png")
	appleTouchIconAsset = mustReadAsset("assets/apple-touch-icon.png")
)

func mustReadAsset(name string) []byte {
	content, err := embeddedFiles.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("read embedded asset %s: %v", name, err))
	}
	return content
}

func joinAssets(names ...string) []byte {
	assets := make([][]byte, 0, len(names))
	for _, name := range names {
		assets = append(assets, mustReadAsset(name))
	}
	return bytes.Join(assets, []byte("\n"))
}

type markdownServer struct {
	root      string
	logger    *log.Logger
	markdown  goldmark.Markdown
	editor    *editorLauncher
	pathToken string

	cacheMu    sync.RWMutex
	titleCache map[string]cachedTitle
}

type cachedTitle struct {
	modTime time.Time
	size    int64
	title   string
}

type pageData struct {
	Title         string
	Directory     string
	Breadcrumbs   []breadcrumb
	Parent        *navEntry
	Directories   []navEntry
	Files         []navEntry
	Body          template.HTML
	TOC           []*tocItem
	TopURL        template.URL
	IsDocument    bool
	ShowTitle     bool
	CanEdit       bool
	Edit          *editAction
	RawURL        template.URL
	StylesheetURL template.URL
	ScriptURL     template.URL
	FaviconSVGURL template.URL
	FaviconICOURL template.URL
	Favicon16URL  template.URL
	Favicon32URL  template.URL
	Favicon48URL  template.URL
	AppleIconURL  template.URL
}

type breadcrumb struct {
	Name string
	URL  template.URL
	Meta string
}

type navEntry struct {
	Name         string
	Title        string
	URL          template.URL
	Modified     string
	ModifiedFull string
	Ago          string
	Size         string
	SortModified int64
	SortSize     int64
	IsDir        bool
	Active       bool
	Edit         *editAction
}

type tocItem struct {
	Title    string
	ID       string
	Level    int
	Children []*tocItem
}

type routeError struct {
	status int
	err    error
}

func (e *routeError) Error() string { return e.err.Error() }
func (e *routeError) Unwrap() error { return e.err }

func newMarkdownServer(root string, logger *log.Logger) (*markdownServer, error) {
	resolved, info, err := resolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", resolved)
	}
	if logger == nil {
		logger = log.New(os.Stderr, "mdfmt: ", log.LstdFlags)
	}
	return &markdownServer{
		root:       resolved,
		logger:     logger,
		markdown:   newGoldmark(),
		titleCache: make(map[string]cachedTitle),
	}, nil
}

// resolveExisting returns the cleaned, symlink-resolved absolute path of an
// existing filesystem entry together with its info.
func resolveExisting(path string) (string, fs.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	return resolved, info, nil
}

func (s *markdownServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header(), s.editor != nil)
	routed, ok := s.stripPathToken(w, r)
	if !ok {
		return
	}
	r = routed
	if r.URL.Path == editEndpoint {
		s.serveEdit(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, assetPrefix) {
		s.serveAsset(w, r)
		return
	}

	components, err := requestComponents(r.URL)
	if err != nil {
		s.writeRouteError(w, r, err)
		return
	}
	resolved, info, err := s.resolve(components)
	if err != nil {
		s.writeRouteError(w, r, err)
		return
	}
	if info.IsDir() {
		if r.URL.Path != "/" && !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, servedURL(s.pathToken, components, true), http.StatusMovedPermanently)
			return
		}
		if err := s.serveDirectory(w, r, resolved, components); err != nil {
			s.writeRouteError(w, r, err)
		}
		return
	}
	if strings.HasSuffix(r.URL.Path, "/") ||
		!info.Mode().IsRegular() ||
		len(components) == 0 {
		s.writeRouteError(w, r, &routeError{status: http.StatusNotFound, err: errors.New("not found")})
		return
	}
	requestedName := components[len(components)-1]
	if isImageName(requestedName) && isImageName(info.Name()) {
		if err := s.serveImage(w, r, resolved, info); err != nil {
			s.writeRouteError(w, r, err)
		}
		return
	}
	if !isMarkdownName(requestedName) || !isMarkdownName(info.Name()) {
		s.writeRouteError(w, r, &routeError{status: http.StatusNotFound, err: errors.New("not found")})
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		if err := s.serveRawMarkdown(w, r, resolved, info); err != nil {
			s.writeRouteError(w, r, err)
		}
		return
	}
	if err := s.serveMarkdown(w, r, resolved, info, components); err != nil {
		s.writeRouteError(w, r, err)
	}
}

func (s *markdownServer) stripPathToken(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if s.pathToken == "" {
		return r, true
	}
	if r.URL.RawPath != "" {
		decoded, err := url.PathUnescape(r.URL.RawPath)
		if err != nil || decoded != r.URL.Path {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return nil, false
		}
	}
	prefix := "/" + s.pathToken
	if r.URL.Path == prefix {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
		} else {
			http.NotFound(w, r)
		}
		return nil, false
	}
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.NotFound(w, r)
		return nil, false
	}
	clone := r.Clone(r.Context())
	clonedURL := *r.URL
	clonedURL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	clonedURL.RawPath = ""
	clone.URL = &clonedURL
	return clone, true
}

func setSecurityHeaders(header http.Header, editing bool) {
	formAction := "'none'"
	if editing {
		formAction = "'self'"
	}
	header.Set("Content-Security-Policy", contentSecurityPolicyWithForm("'self'", "'self'", "'self' data:", true, formAction))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
}

// contentSecurityPolicy states mdfmt's policy once for both delivery paths. Set
// frameAncestors only for a response header: browsers ignore the directive (and
// report an error) when the policy is delivered in a <meta> tag.
func contentSecurityPolicy(styleSrc, scriptSrc, imgSrc string, frameAncestors bool) string {
	return contentSecurityPolicyWithForm(styleSrc, scriptSrc, imgSrc, frameAncestors, "'none'")
}

func contentSecurityPolicyWithForm(styleSrc, scriptSrc, imgSrc string, frameAncestors bool, formAction string) string {
	directives := []string{
		"default-src 'none'",
		"style-src " + styleSrc,
		"script-src " + scriptSrc,
		"img-src " + imgSrc,
		"object-src 'none'",
		"base-uri 'none'",
	}
	if frameAncestors {
		directives = append(directives, "frame-ancestors 'none'")
	}
	return strings.Join(append(directives, "form-action "+formAction), "; ")
}

func (s *markdownServer) serveAsset(w http.ResponseWriter, r *http.Request) {
	var (
		content     []byte
		contentType string
	)
	switch r.URL.Path {
	case assetPrefix + "style.css":
		content, contentType = stylesheetAsset, "text/css; charset=utf-8"
	case assetPrefix + "app.js":
		content, contentType = scriptAsset, "text/javascript; charset=utf-8"
	case assetPrefix + "favicon.svg":
		content, contentType = faviconSVGAsset, "image/svg+xml"
	case assetPrefix + "favicon.ico":
		content, contentType = faviconICOAsset, "image/x-icon"
	case assetPrefix + "favicon-16.png":
		content, contentType = favicon16Asset, "image/png"
	case assetPrefix + "favicon-32.png":
		content, contentType = favicon32Asset, "image/png"
	case assetPrefix + "favicon-48.png":
		content, contentType = favicon48Asset, "image/png"
	case assetPrefix + "apple-touch-icon.png":
		content, contentType = appleTouchIconAsset, "image/png"
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodGet {
		_, _ = w.Write(content)
	}
}

func requestComponents(u *url.URL) ([]string, error) {
	if u.RawPath != "" {
		decoded, err := url.PathUnescape(u.RawPath)
		if err != nil || decoded != u.Path {
			return nil, &routeError{status: http.StatusBadRequest, err: errors.New("malformed URL path")}
		}
	}
	escaped := u.EscapedPath()
	if escaped == "" || escaped[0] != '/' {
		return nil, &routeError{status: http.StatusBadRequest, err: errors.New("URL path must be absolute")}
	}
	if escaped == "/" {
		return nil, nil
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(escaped, "/"), "/"), "/")
	components := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, &routeError{status: http.StatusBadRequest, err: errors.New("malformed URL path")}
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, &routeError{status: http.StatusBadRequest, err: errors.New("malformed URL escape")}
		}
		switch {
		case decoded == "." || decoded == "..":
			return nil, &routeError{status: http.StatusForbidden, err: errors.New("path traversal rejected")}
		case strings.ContainsAny(decoded, "/\\\x00"):
			return nil, &routeError{status: http.StatusForbidden, err: errors.New("invalid path component")}
		case strings.HasPrefix(decoded, "."):
			return nil, &routeError{status: http.StatusNotFound, err: errors.New("not found")}
		}
		components = append(components, decoded)
	}
	return components, nil
}

func (s *markdownServer) resolve(components []string) (string, fs.FileInfo, error) {
	candidate := s.root
	for _, component := range components {
		candidate = filepath.Join(candidate, component)
	}
	if !withinRoot(s.root, candidate) {
		return "", nil, &routeError{status: http.StatusForbidden, err: errors.New("path escapes root")}
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, fileError(err)
	}
	if !withinRoot(s.root, resolved) {
		return "", nil, &routeError{status: http.StatusForbidden, err: errors.New("symlink escapes root")}
	}
	if hasHiddenComponent(s.root, resolved) {
		return "", nil, &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fileError(err)
	}
	return resolved, info, nil
}

func withinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func hasHiddenComponent(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return true
	}
	if relative == "." {
		return false
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if strings.HasPrefix(component, ".") {
			return true
		}
	}
	return false
}

func fileError(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	case errors.Is(err, fs.ErrPermission):
		return &routeError{status: http.StatusForbidden, err: errors.New("permission denied")}
	default:
		return &routeError{status: http.StatusInternalServerError, err: err}
	}
}

func (s *markdownServer) serveDirectory(w http.ResponseWriter, r *http.Request, directory string, components []string) error {
	directories, files, err := s.listDirectory(directory, components, "", true)
	if err != nil {
		return err
	}
	name := filepath.Base(directory)
	if len(components) > 0 {
		name = components[len(components)-1]
	}
	data := pageData{
		Title:       name,
		Directory:   name,
		Breadcrumbs: s.breadcrumbsFor(components, nil),
		Parent:      s.parentEntry(components),
		Directories: directories,
		Files:       files,
		ShowTitle:   true,
		CanEdit:     s.editor != nil,
	}
	s.setPageAssetURLs(&data, components)
	return s.writePage(w, r, data)
}

func (s *markdownServer) serveMarkdown(w http.ResponseWriter, r *http.Request, filename string, info fs.FileInfo, components []string) error {
	source, err := os.ReadFile(filename)
	if err != nil {
		return fileError(err)
	}
	rendered, err := renderMarkdownDocumentForServe(s.markdown, source, filename, components[:len(components)-1], s.pathToken != "")
	if err != nil {
		return &routeError{status: http.StatusInternalServerError, err: fmt.Errorf("render markdown: %w", err)}
	}

	directoryComponents := components[:len(components)-1]
	directory, directoryInfo, err := s.resolve(directoryComponents)
	if err != nil {
		return err
	}
	if !directoryInfo.IsDir() {
		return &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	}
	directories, files, err := s.listDirectory(directory, directoryComponents, components[len(components)-1], false)
	if err != nil {
		return err
	}
	rawURL := s.pageURL(directoryComponents, components, false) + "?raw=1"
	if s.pathToken != "" {
		rawURL = "?raw=1"
	}
	data := pageData{
		Title:     rendered.Title,
		Directory: directoryName(s.root, directoryComponents),
		Breadcrumbs: s.breadcrumbsFor(directoryComponents, &breadcrumb{
			Name: components[len(components)-1],
			Meta: fileBreadcrumbMeta(info, time.Now()),
		}),
		Parent:      s.parentEntry(directoryComponents),
		Directories: directories,
		Files:       files,
		Body:        rendered.Body,
		TOC:         rendered.TOC,
		TopURL:      template.URL(s.pageURL(directoryComponents, components, false)),
		IsDocument:  true,
		ShowTitle:   !rendered.HasH1,
		CanEdit:     s.editor != nil,
		Edit:        s.editAction(components, directoryComponents, components, false),
		RawURL:      template.URL(rawURL),
	}
	s.setPageAssetURLs(&data, directoryComponents)
	return s.writePage(w, r, data)
}

func (s *markdownServer) setPageAssetURLs(data *pageData, baseDirectory []string) {
	assetURL := func(name string) template.URL {
		return template.URL(s.pageURL(baseDirectory, []string{".mdfmt", name}, false))
	}
	data.StylesheetURL = template.URL(string(assetURL("style.css")) + "?v=6")
	data.ScriptURL = template.URL(string(assetURL("app.js")) + "?v=5")
	data.FaviconSVGURL = assetURL("favicon.svg")
	data.FaviconICOURL = assetURL("favicon.ico")
	data.Favicon16URL = assetURL("favicon-16.png")
	data.Favicon32URL = assetURL("favicon-32.png")
	data.Favicon48URL = assetURL("favicon-48.png")
	data.AppleIconURL = assetURL("apple-touch-icon.png")
}

func (s *markdownServer) serveRawMarkdown(w http.ResponseWriter, r *http.Request, filename string, info fs.FileInfo) error {
	source, err := os.ReadFile(filename)
	if err != nil {
		return fileError(err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(filename), info.ModTime(), bytes.NewReader(source))
	return nil
}

func (s *markdownServer) serveImage(w http.ResponseWriter, r *http.Request, filename string, info fs.FileInfo) error {
	contentType, ok := imageContentType(filename)
	if !ok {
		return &routeError{status: http.StatusNotFound, err: errors.New("not found")}
	}
	file, err := os.Open(filename)
	if err != nil {
		return fileError(err)
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(filename), info.ModTime(), file)
	return nil
}

func (s *markdownServer) writePage(w http.ResponseWriter, r *http.Request, data pageData) error {
	var output bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&output, "page.html", data); err != nil {
		return &routeError{status: http.StatusInternalServerError, err: fmt.Errorf("execute template: %w", err)}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = w.Write(output.Bytes())
	}
	return nil
}

func newGoldmark() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			mdhighlight.Extension(),
			safeRawHTMLExtension{},
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
}

type renderedDocument struct {
	Title string
	Body  template.HTML
	TOC   []*tocItem
	HasH1 bool
}

func renderMarkdownDocument(markdown goldmark.Markdown, source []byte, filename string) (renderedDocument, error) {
	return renderMarkdownDocumentWithRootLinks(markdown, source, filename, nil, false)
}

func renderMarkdownDocumentForServe(
	markdown goldmark.Markdown,
	source []byte,
	filename string,
	directoryComponents []string,
	rewriteRootLinks bool,
) (renderedDocument, error) {
	return renderMarkdownDocumentWithRootLinks(
		markdown,
		source,
		filename,
		directoryComponents,
		rewriteRootLinks,
	)
}

func renderMarkdownDocumentWithRootLinks(
	markdown goldmark.Markdown,
	source []byte,
	filename string,
	directoryComponents []string,
	rewriteRootLinks bool,
) (renderedDocument, error) {
	document := markdown.Parser().Parse(text.NewReader(source))
	rendered := renderedDocument{Title: stem(filepath.Base(filename))}
	headings := make([]*ast.Heading, 0)
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			annotateSafeRawHTML(node, source, directoryComponents, rewriteRootLinks)
		}
		if entering && rewriteRootLinks {
			switch node := node.(type) {
			case *ast.Link:
				node.Destination = rewriteRootRelativeDestination(node.Destination, directoryComponents)
			case *ast.Image:
				node.Destination = rewriteRootRelativeDestination(node.Destination, directoryComponents)
			}
		}
		heading, ok := node.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		if heading.Level == 1 && !rendered.HasH1 {
			rendered.HasH1 = true
			if headingTitle := strings.TrimSpace(extractText(source, heading)); headingTitle != "" {
				rendered.Title = headingTitle
			}
		}
		if heading.Level >= 1 && heading.Level <= 4 {
			headings = append(headings, heading)
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return renderedDocument{}, err
	}

	rendered.TOC = buildTOC(source, headings)
	var output bytes.Buffer
	if err := markdown.Renderer().Render(&output, source, document); err != nil {
		return renderedDocument{}, err
	}
	// Goldmark escapes Markdown content. The custom raw HTML renderer emits only
	// normalized, allowlisted anchors and images.
	rendered.Body = template.HTML(output.Bytes())
	return rendered, nil
}

func rewriteRootRelativeDestination(destination []byte, directoryComponents []string) []byte {
	raw := string(destination)
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return destination
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "" || target.Host != "" || target.User != nil {
		return destination
	}
	components, err := requestComponents(target)
	if err != nil {
		return destination
	}
	rewritten := relativeURL(directoryComponents, components, strings.HasSuffix(target.Path, "/"))
	if target.ForceQuery || target.RawQuery != "" {
		rewritten += "?" + target.RawQuery
	}
	if target.Fragment != "" {
		rewritten += "#" + target.EscapedFragment()
	}
	return []byte(rewritten)
}

func buildTOC(source []byte, headings []*ast.Heading) []*tocItem {
	var roots []*tocItem
	var stack []*tocItem
	for _, heading := range headings {
		idValue, ok := heading.AttributeString("id")
		if !ok {
			continue
		}
		id, ok := idValue.([]byte)
		if !ok {
			continue
		}
		item := &tocItem{
			Title: extractText(source, heading),
			ID:    string(id),
			Level: heading.Level,
		}
		for len(stack) > 0 && stack[len(stack)-1].Level >= item.Level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, item)
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, item)
		}
		stack = append(stack, item)
	}
	return roots
}

func (s *markdownServer) listDirectory(directory string, components []string, active string, titles bool) ([]navEntry, []navEntry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, fileError(err)
	}
	var directories []navEntry
	var files []navEntry
	now := time.Now()
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(directory, name))
		if err != nil || !withinRoot(s.root, resolved) || hasHiddenComponent(s.root, resolved) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		entryComponents := appendComponents(components, name)
		switch {
		case info.IsDir():
			directories = append(directories, navEntry{
				Name:  name,
				Title: name,
				URL:   template.URL(s.pageURL(components, entryComponents, true)),
				IsDir: true,
			})
		case info.Mode().IsRegular() && isMarkdownName(name) && isMarkdownName(info.Name()):
			title := stem(name)
			if titles {
				documentTitle, err := s.titleFor(resolved, info)
				if err != nil {
					s.logger.Printf("read title %s: %v", resolved, err)
				} else {
					title = documentTitle
				}
			}
			files = append(files, navEntry{
				Name:         name,
				Title:        title,
				URL:          template.URL(s.pageURL(components, entryComponents, false)),
				Modified:     info.ModTime().Format("Jan 2, 2006 15:04"),
				ModifiedFull: info.ModTime().Format(time.RFC3339),
				Ago:          humanAgo(info.ModTime(), now),
				Size:         humanSize(info.Size()),
				SortModified: info.ModTime().UnixNano(),
				SortSize:     info.Size(),
				Active:       name == active,
				Edit:         s.editAction(entryComponents, components, components, true),
			})
		}
	}
	sortEntries(directories)
	sortEntries(files)
	return directories, files, nil
}

func (s *markdownServer) titleFor(filename string, info fs.FileInfo) (string, error) {
	s.cacheMu.RLock()
	cached, ok := s.titleCache[filename]
	s.cacheMu.RUnlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.title, nil
	}
	source, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	title := extractTitle(s.markdown, source, filename)
	s.cacheMu.Lock()
	s.titleCache[filename] = cachedTitle{modTime: info.ModTime(), size: info.Size(), title: title}
	s.cacheMu.Unlock()
	return title, nil
}

func extractTitle(markdown goldmark.Markdown, source []byte, filename string) string {
	document := markdown.Parser().Parse(text.NewReader(source))
	title := stem(filepath.Base(filename))
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		heading, ok := node.(*ast.Heading)
		if entering && ok && heading.Level == 1 {
			if headingTitle := strings.TrimSpace(extractText(source, heading)); headingTitle != "" {
				title = headingTitle
			}
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return title
}

// extractText flattens the text descendants of a Markdown AST node. Goldmark
// represents emphasized text and links as nested inline nodes, so heading
// titles and table-of-contents labels need a recursive walk.
func extractText(source []byte, node ast.Node) string {
	var text strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch child := child.(type) {
		case *ast.Text:
			text.Write(child.Segment.Value(source))
		case *ast.String:
			text.Write(child.Value)
		default:
			text.WriteString(extractText(source, child))
		}
	}
	return text.String()
}

func breadcrumbsFor(root string, directoryComponents []string, document *breadcrumb) []breadcrumb {
	crumbs := []breadcrumb{{
		Name: filepath.Base(root),
		URL:  template.URL("/"),
	}}
	for i, component := range directoryComponents {
		crumbs = append(crumbs, breadcrumb{
			Name: component,
			URL:  template.URL(escapedURL(directoryComponents[:i+1], true)),
		})
	}
	if document != nil {
		crumbs = append(crumbs, *document)
	}
	return crumbs
}

func (s *markdownServer) breadcrumbsFor(directoryComponents []string, document *breadcrumb) []breadcrumb {
	crumbs := []breadcrumb{{
		Name: filepath.Base(s.root),
		URL:  template.URL(s.pageURL(directoryComponents, nil, true)),
	}}
	for i, component := range directoryComponents {
		crumbs = append(crumbs, breadcrumb{
			Name: component,
			URL:  template.URL(s.pageURL(directoryComponents, directoryComponents[:i+1], true)),
		})
	}
	if document != nil {
		crumbs = append(crumbs, *document)
	}
	return crumbs
}

func (s *markdownServer) parentEntry(components []string) *navEntry {
	if len(components) == 0 {
		return nil
	}
	name := "Parent directory"
	return &navEntry{
		Name:  name,
		Title: name,
		URL:   template.URL(s.pageURL(components, components[:len(components)-1], true)),
		IsDir: true,
	}
}

func (s *markdownServer) pageURL(baseDirectory, target []string, directory bool) string {
	if s.pathToken == "" {
		return escapedURL(target, directory)
	}
	// Relative URLs inherit the secret prefix from the current browser URL
	// without placing that prefix in the generated HTML.
	return relativeURL(baseDirectory, target, directory)
}

func relativeURL(baseDirectory, target []string, directory bool) string {
	common := 0
	for common < len(baseDirectory) && common < len(target) && baseDirectory[common] == target[common] {
		common++
	}
	parts := make([]string, 0, len(baseDirectory)-common+len(target)-common)
	for i := common; i < len(baseDirectory); i++ {
		parts = append(parts, "..")
	}
	for _, component := range target[common:] {
		parts = append(parts, url.PathEscape(component))
	}
	if len(parts) == 0 {
		if directory {
			return "./"
		}
		return ""
	}
	result := strings.Join(parts, "/")
	if directory {
		result += "/"
	}
	return result
}

func directoryName(root string, components []string) string {
	if len(components) == 0 {
		return filepath.Base(root)
	}
	return components[len(components)-1]
}

func escapedURL(components []string, directory bool) string {
	if len(components) == 0 {
		return "/"
	}
	escaped := make([]string, len(components))
	for i, component := range components {
		escaped[i] = url.PathEscape(component)
	}
	result := "/" + strings.Join(escaped, "/")
	if directory {
		result += "/"
	}
	return result
}

func appendComponents(components []string, name string) []string {
	result := make([]string, len(components)+1)
	copy(result, components)
	result[len(components)] = name
	return result
}

func sortEntries(entries []navEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if left == right {
			return entries[i].Name < entries[j].Name
		}
		return left < right
	})
}

func humanAgo(then, now time.Time) string {
	if then.After(now) {
		return "just now"
	}
	elapsed := now.Sub(then)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return agoPhrase(int(elapsed/time.Minute), "minute")
	case elapsed < 24*time.Hour:
		return agoPhrase(int(elapsed/time.Hour), "hour")
	case elapsed < 7*24*time.Hour:
		return agoPhrase(int(elapsed/(24*time.Hour)), "day")
	case elapsed < 30*24*time.Hour:
		return agoPhrase(int(elapsed/(7*24*time.Hour)), "week")
	case elapsed < 365*24*time.Hour:
		return agoPhrase(int(elapsed/(30*24*time.Hour)), "month")
	default:
		return agoPhrase(int(elapsed/(365*24*time.Hour)), "year")
	}
}

func fileBreadcrumbMeta(info fs.FileInfo, now time.Time) string {
	return fmt.Sprintf(
		"%s, %s, %s",
		humanAgoCompact(info.ModTime(), now),
		info.ModTime().Local().Format("Jan 2, 2006 3:04 PM"),
		humanSize(info.Size()),
	)
}

func humanAgoCompact(then, now time.Time) string {
	if then.After(now) {
		return "just now"
	}
	elapsed := now.Sub(then)
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed/time.Minute))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed/time.Hour))
	case elapsed < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(elapsed/(24*time.Hour)))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(elapsed/(7*24*time.Hour)))
	case elapsed < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(elapsed/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(elapsed/(365*24*time.Hour)))
	}
}

func agoPhrase(value int, unit string) string {
	if value != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s ago", value, unit)
}

func humanSize(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	divisor := unit
	exponent := 0
	for value := size / unit; value >= unit && exponent < 5; value /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(divisor), "KMGTPE"[exponent])
}

func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func isMarkdownName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func isImageName(name string) bool {
	_, ok := imageContentType(name)
	return ok
}

func imageContentType(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	default:
		return "", false
	}
}

func (s *markdownServer) writeRouteError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	var routeErr *routeError
	if errors.As(err, &routeErr) {
		status = routeErr.status
	}
	if status >= http.StatusInternalServerError {
		s.logger.Printf("%s %s: %v", r.Method, r.URL.EscapedPath(), err)
	} else if status == http.StatusForbidden || status == http.StatusBadRequest {
		s.logger.Printf("%s %s: rejected (%d)", r.Method, r.URL.EscapedPath(), status)
	}
	message := http.StatusText(status)
	if status == http.StatusNotFound {
		message = "not found"
	}
	http.Error(w, message, status)
}
