package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, root string) *markdownServer {
	t.Helper()
	server, err := newMarkdownServer(root, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func writeTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return filename
}

func request(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestMarkdownRenderingHeadingIDsAndTOC(t *testing.T) {
	root := t.TempDir()
	filename := writeTestFile(t, root, "guide.md", "# Guide\n\nIntro with **strong text**.\n\n## Install Here\n\nText.\n\n### Options\n\n#### Detail\n\n## Install Here\n\n# Appendix\n\n## More\n")
	server := testServer(t, root)

	rendered, err := renderMarkdownDocument(server.markdown, []byte(mustRead(t, filename)), filename)
	if err != nil {
		t.Fatal(err)
	}
	title, body, toc := rendered.Title, string(rendered.Body), rendered.TOC
	if title != "Guide" {
		t.Fatalf("title = %q, want Guide", title)
	}
	if !rendered.HasH1 {
		t.Fatal("rendered document did not report its h1 headings")
	}
	for _, want := range []string{
		"<strong>strong text</strong>",
		`<h1 id="guide">Guide</h1>`,
		`<h2 id="install-here">Install Here</h2>`,
		`<h3 id="options">Options</h3>`,
		`<h4 id="detail">Detail</h4>`,
		`<h2 id="install-here-1">Install Here</h2>`,
		`<h1 id="appendix">Appendix</h1>`,
		`<h2 id="more">More</h2>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body does not contain %q:\n%s", want, body)
		}
	}
	if len(toc) != 2 || toc[0].ID != "guide" || toc[1].ID != "appendix" {
		t.Fatalf("h1 headings are not top-level TOC entries: %#v", toc)
	}
	if len(toc[0].Children) != 2 || toc[0].Children[0].ID != "install-here" || toc[0].Children[1].ID != "install-here-1" {
		t.Fatalf("h2 headings are not nested beneath the first h1: %#v", toc[0].Children)
	}
	if len(toc[0].Children[0].Children) != 1 || toc[0].Children[0].Children[0].Title != "Options" {
		t.Fatalf("h3 is not nested beneath h2: %#v", toc[0].Children[0].Children)
	}
	if len(toc[0].Children[0].Children[0].Children) != 1 || toc[0].Children[0].Children[0].Children[0].Title != "Detail" {
		t.Fatalf("h4 is not nested beneath h3: %#v", toc[0].Children[0].Children[0].Children)
	}
	if len(toc[1].Children) != 1 || toc[1].Children[0].ID != "more" {
		t.Fatalf("second h1 did not retain its h2: %#v", toc[1].Children)
	}
}

func TestFencedCodeSyntaxHighlighting(t *testing.T) {
	tests := []struct {
		name     string
		language string
		source   string
		want     []string
	}{
		{
			name:     "python",
			language: "python",
			source:   `def greet(name): return "<script>&"`,
			want:     []string{`data-language="python"`, `class="k">def</span>`, `class="nf">greet</span>`},
		},
		{
			name:     "javascript alias",
			language: "js",
			source:   `const answer = 42;`,
			want:     []string{`data-language="js"`, `class="kr">const</span>`, `class="mi">42</span>`},
		},
		{
			name:     "typescript alias",
			language: "ts",
			source:   `const answer: number = 42;`,
			want:     []string{`data-language="ts"`, `class="kr">const</span>`},
		},
		{
			name:     "sql",
			language: "sql",
			source:   `SELECT name FROM people;`,
			want:     []string{`data-language="sql"`, `class="k">SELECT</span>`, `class="k">FROM</span>`},
		},
		{
			name:     "nim",
			language: "nim",
			source:   `let answer = 42`,
			want:     []string{`data-language="nim"`, `class="k">let</span>`},
		},
		{
			name:     "starlark",
			language: "starlark",
			source:   `def rule(name): return name`,
			want:     []string{`data-language="starlark"`, `class="k">def</span>`},
		},
		{
			name:     "qeylan",
			language: "qeylan",
			source:   `let amount = 12days // note`,
			want:     []string{`data-language="qeylan"`, `class="kd">let</span>`, `class="m">12days</span>`, `class="c1">// note</span>`},
		},
		{
			name:     "qeylan alias",
			language: "qy",
			source:   `if ready? return`,
			want:     []string{`data-language="qy"`, `class="k">if</span>`, `class="o">?</span>`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdownSource := "```" + tt.language + "\n" + tt.source + "\n```\n"
			rendered, err := renderMarkdownDocument(newGoldmark(), []byte(markdownSource), "example.md")
			if err != nil {
				t.Fatal(err)
			}
			body := string(rendered.Body)
			for _, common := range []string{`<div class="highlight"`, `<pre class="chroma"><code>`, `class="line"`, `class="cl"`} {
				if !strings.Contains(body, common) {
					t.Errorf("highlighted %s does not contain %q:\n%s", tt.language, common, body)
				}
			}
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("highlighted %s does not contain %q:\n%s", tt.language, want, body)
				}
			}
			for _, forbidden := range []string{`style="`, `<script>`, `"<script>`, `&</span>`} {
				if strings.Contains(body, forbidden) {
					t.Errorf("highlighted %s contains unsafe/unwanted %q:\n%s", tt.language, forbidden, body)
				}
			}
			if strings.Contains(tt.source, "<script>") && (!strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "&amp;")) {
				t.Errorf("highlighted source was not HTML escaped:\n%s", body)
			}
		})
	}
}

func TestFencedCodeFallbacksRemainPlainAndEscaped(t *testing.T) {
	source := "```unknown\"onclick=\"bad\n<script>&\n```\n\n```\nplain <&>\n```\n\n    indented <&>\n\nInline `<&>`.\n"
	rendered, err := renderMarkdownDocument(newGoldmark(), []byte(source), "fallbacks.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(rendered.Body)
	for _, want := range []string{
		`data-language="unknown&#34;onclick=&#34;bad"`,
		`class="language-unknown&#34;onclick=&#34;bad"`,
		"&lt;script&gt;&amp;",
		"<pre><code>plain &lt;&amp;&gt;",
		"<pre><code>indented &lt;&amp;&gt;",
		"<code>&lt;&amp;&gt;</code>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fallback output does not contain %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{`<script>`, `onclick="bad"`, `class="highlight"`, `class="chroma"`, `<span class=`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("fallback output contains %q:\n%s", forbidden, body)
		}
	}
}

func TestTitleExtraction(t *testing.T) {
	root := t.TempDir()
	server := testServer(t, root)

	if got := extractTitle(server.markdown, []byte("intro\n\n# The *Readable* Title\n"), "fallback.md"); got != "The Readable Title" {
		t.Errorf("first h1 title = %q", got)
	}
	if got := extractTitle(server.markdown, []byte("## No level one\n"), "Fallback.MARKDOWN"); got != "Fallback" {
		t.Errorf("fallback title = %q, want Fallback", got)
	}
}

func TestDirectorySortingAndTitles(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"zeta", "Beta", "alpha"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, root, "z.md", "# Last\n")
	writeTestFile(t, root, "A.markdown", "# First Readable\n")
	writeTestFile(t, root, "middle.MD", "No h1.\n")
	writeTestFile(t, root, "ignored.txt", "do not list")

	server := testServer(t, root)
	directories, files, err := server.listDirectory(server.root, nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(directories); got != "alpha,Beta,zeta" {
		t.Errorf("directories = %s", got)
	}
	if got := entryNames(files); got != "A.markdown,middle.MD,z.md" {
		t.Errorf("files = %s", got)
	}
	if files[0].Title != "First Readable" || files[1].Title != "middle" || files[2].Title != "Last" {
		t.Errorf("unexpected display titles: %#v", files)
	}
}

func TestHumanReadableFileMetadata(t *testing.T) {
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		then time.Time
		want string
	}{
		{"recent", now.Add(-20 * time.Second), "just now"},
		{"one minute", now.Add(-time.Minute), "1 minute ago"},
		{"hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"day", now.Add(-24 * time.Hour), "1 day ago"},
		{"weeks", now.Add(-15 * 24 * time.Hour), "2 weeks ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanAgo(tt.then, now); got != tt.want {
				t.Errorf("humanAgo() = %q, want %q", got, tt.want)
			}
		})
	}

	sizeTests := map[int64]string{
		0:           "0 B",
		999:         "999 B",
		1024:        "1.0 KiB",
		1536:        "1.5 KiB",
		1024 * 1024: "1.0 MiB",
	}
	for size, want := range sizeTests {
		if got := humanSize(size); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestBreadcrumbConstruction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "docs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	crumbs := breadcrumbsFor(root, []string{"plans", "next release"}, &breadcrumb{
		Name: "overview.md",
		Meta: "3h ago, Jul 28, 2026 3:00 PM, 1.2 KiB",
	})
	if len(crumbs) != 4 {
		t.Fatalf("got %d breadcrumbs, want 4", len(crumbs))
	}
	if crumbs[0].Name != "docs" || crumbs[0].URL != "/" {
		t.Errorf("root crumb = %#v", crumbs[0])
	}
	if crumbs[2].Name != "next release" || crumbs[2].URL != "/plans/next%20release/" {
		t.Errorf("nested crumb = %#v", crumbs[2])
	}
	if crumbs[3].Name != "overview.md" || crumbs[3].URL != "" || crumbs[3].Meta != "3h ago, Jul 28, 2026 3:00 PM, 1.2 KiB" {
		t.Errorf("document crumb = %#v", crumbs[3])
	}
}

func TestFileAndDirectoryRouting(t *testing.T) {
	root := t.TempDir()
	guideContent := "# Guide\n\nHello, docs.\n"
	guidePath := writeTestFile(t, root, "guide.md", guideContent)
	guideModified := time.Now().Add(-3*time.Hour - 30*time.Minute)
	if err := os.Chtimes(guidePath, guideModified, guideModified); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "with space/child.markdown", "# Child\n")
	server := testServer(t, root)

	t.Run("root directory", func(t *testing.T) {
		response := request(t, server, "/")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		if !strings.Contains(response.Body.String(), "Guide") || !strings.Contains(response.Body.String(), "with space") {
			t.Errorf("root listing is incomplete:\n%s", response.Body.String())
		}
		for _, want := range []string{"Filename", "Last modified", "Ago", "Size", "Title", `data-sort-key="name"`, `data-sort-key="modified"`, `data-sort-key="size"`} {
			if !strings.Contains(response.Body.String(), want) {
				t.Errorf("root table does not contain %q:\n%s", want, response.Body.String())
			}
		}
		if strings.Contains(response.Body.String(), `<span class="icon">#</span>`) {
			t.Error("directory table still displays a # file icon")
		}
	})

	t.Run("directory redirects to slash", func(t *testing.T) {
		response := request(t, server, "/with%20space")
		if response.Code != http.StatusMovedPermanently {
			t.Fatalf("status = %d, want 301", response.Code)
		}
		if location := response.Header().Get("Location"); location != "/with%20space/" {
			t.Errorf("Location = %q", location)
		}
	})

	t.Run("directory with slash", func(t *testing.T) {
		response := request(t, server, "/with%20space/")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Child") {
			t.Errorf("directory response: status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("ordinary markdown file", func(t *testing.T) {
		response := request(t, server, "/guide.md")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		body := response.Body.String()
		if !strings.Contains(body, "Hello, docs.") || strings.Count(body, `<h1 id="guide">Guide</h1>`) != 1 {
			t.Errorf("unexpected file page:\n%s", body)
		}
		if strings.Contains(body, `<h1 class="page-title">Guide</h1>`) {
			t.Error("document h1 is displayed twice")
		}
		if !strings.Contains(body, `aria-current="page"`) {
			t.Errorf("active file is not marked in navigation:\n%s", body)
		}
		if !strings.Contains(body, `class="toc-top" href="/guide.md" data-top`) {
			t.Errorf("file page has no fragment-clearing top link:\n%s", body)
		}
		if !strings.Contains(body, `class="raw-link" href="/guide.md?raw=1"`) {
			t.Errorf("file page has no raw Markdown link:\n%s", body)
		}
		for _, want := range []string{
			`aria-current="page">guide.md`,
			`class="breadcrumb-meta">(3h ago,`,
			guideModified.Local().Format("Jan 2, 2006 3:04 PM"),
			humanSize(int64(len(guideContent))),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("file breadcrumb does not contain %q:\n%s", want, body)
			}
		}
	})
}

func TestRawMarkdownRouting(t *testing.T) {
	root := t.TempDir()
	source := "# Exact source\r\n\r\n```go\r\nfmt.Println(\"copy me\")\r\n```\r\n\r\n<script>plain text only</script>\r\n"
	writeTestFile(t, root, "with space/Guide.MD", source)
	writeTestFile(t, root, "notes.txt", "not Markdown")
	writeTestFile(t, root, ".hidden.md", "# Hidden\n")
	server := testServer(t, root)

	response := request(t, server, "/with%20space/Guide.MD?raw=1")
	if response.Code != http.StatusOK {
		t.Fatalf("raw status = %d; body=%s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != source {
		t.Errorf("raw body does not exactly match source:\ngot  %q\nwant %q", got, source)
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	req := httptest.NewRequest(http.MethodHead, "/with%20space/Guide.MD?raw=1", nil)
	head := httptest.NewRecorder()
	server.ServeHTTP(head, req)
	if head.Code != http.StatusOK {
		t.Fatalf("raw HEAD status = %d", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("raw HEAD returned a body: %q", head.Body.String())
	}

	for _, target := range []string{
		"/notes.txt?raw=1",
		"/.hidden.md?raw=1",
		"/%2e%2e/outside.md?raw=1",
	} {
		rejected := request(t, server, target)
		if rejected.Code == http.StatusOK {
			t.Errorf("raw route exposed rejected path %q", target)
		}
		if strings.Contains(rejected.Body.String(), "not Markdown") ||
			strings.Contains(rejected.Body.String(), "# Hidden") {
			t.Errorf("raw rejection exposed contents for %q", target)
		}
	}
}

func TestRejections(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "docs")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ".hidden.md", "# Hidden\n")
	writeTestFile(t, root, ".private/secret.md", "# Secret\n")
	writeTestFile(t, root, "notes.txt", "private non-markdown content")
	writeTestFile(t, parent, "outside.md", "# Outside\n")
	server := testServer(t, root)

	tests := []struct {
		name   string
		target string
		status int
	}{
		{"hidden file", "/.hidden.md", http.StatusNotFound},
		{"hidden directory", "/.private/secret.md", http.StatusNotFound},
		{"non markdown", "/notes.txt", http.StatusNotFound},
		{"plain traversal", "/../outside.md", http.StatusForbidden},
		{"encoded traversal", "/%2e%2e/outside.md", http.StatusForbidden},
		{"encoded slash traversal", "/%2e%2e%2foutside.md", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := request(t, server, tt.target)
			if response.Code != tt.status {
				t.Errorf("status = %d, want %d; body=%s", response.Code, tt.status, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private non-markdown content") {
				t.Error("rejection exposed file contents")
			}
		})
	}
}

func TestMalformedURLEscape(t *testing.T) {
	_, err := requestComponents(&url.URL{Path: "/bad", RawPath: "/%zz"})
	var routeErr *routeError
	if !errorsAs(err, &routeErr) || routeErr.status != http.StatusBadRequest {
		t.Fatalf("error = %v, want 400 route error", err)
	}
}

func TestSymlinksStayWithinRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "docs")
	sibling := filepath.Join(parent, "docs-secret")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "real.md", "# Real\n")
	writeTestFile(t, root, "plain.txt", "non-markdown secret\n")
	writeTestFile(t, root, ".internal.md", "# Hidden Target\n")
	writeTestFile(t, sibling, "secret.md", "# Sibling Secret\n")
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "inside.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(sibling, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "plain.txt"), filepath.Join(root, "disguised.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".internal.md"), filepath.Join(root, "visible.md")); err != nil {
		t.Fatal(err)
	}
	server := testServer(t, root)

	if response := request(t, server, "/inside.md"); response.Code != http.StatusOK {
		t.Errorf("in-root symlink status = %d, want 200", response.Code)
	}
	response := request(t, server, "/escape.md")
	if response.Code != http.StatusForbidden {
		t.Errorf("similarly-prefixed sibling symlink status = %d, want 403", response.Code)
	}
	if strings.Contains(response.Body.String(), "Sibling Secret") {
		t.Error("escaping symlink exposed sibling content")
	}
	if withinRoot(root, sibling) {
		t.Error("similarly-prefixed sibling considered contained")
	}
	if response := request(t, server, "/disguised.md"); response.Code != http.StatusNotFound {
		t.Errorf("Markdown alias to non-Markdown target status = %d, want 404", response.Code)
	}
	if response := request(t, server, "/visible.md"); response.Code != http.StatusNotFound {
		t.Errorf("visible alias to hidden target status = %d, want 404", response.Code)
	}
}

func TestRawHTMLIsNotEmitted(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "unsafe.md", "# Safe\n\n<script>alert(1)</script>\n\n<div>raw</div>\n")
	response := request(t, testServer(t, root), "/unsafe.md")
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, forbidden := range []string{"<script>", "<div>raw</div>", "raw HTML omitted"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response contains unsafe/raw marker %q:\n%s", forbidden, body)
		}
	}
}

func TestSecurityHeadersAndReadOnlyMethods(t *testing.T) {
	root := t.TempDir()
	server := testServer(t, root)
	response := request(t, server, "/")
	for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if response.Header().Get(name) == "" {
			t.Errorf("missing %s", name)
		}
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'none'") {
		t.Errorf("read-only CSP unexpectedly permits forms: %q", csp)
	}

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("data"))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", recorder.Code)
	}
}

func TestEmbeddedAssetsRevalidateAndIncludePersistentSort(t *testing.T) {
	root := t.TempDir()
	server := testServer(t, root)
	response := request(t, server, "/.mdfmt/app.js?v=3")
	if response.Code != http.StatusOK {
		t.Fatalf("asset status = %d", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	body := response.Body.String()
	for _, want := range []string{"mdfmt.directory-sort", "mdfmt_directory_sort", "localStorage", "document.cookie"} {
		if !strings.Contains(body, want) {
			t.Errorf("sorting script does not contain %q", want)
		}
	}
}

func TestServedStylesheetIncludesSyntaxThemes(t *testing.T) {
	root := t.TempDir()
	response := request(t, testServer(t, root), "/.mdfmt/style.css?v=6")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, want := range []string{
		"body {",
		"Code generated by go generate ./internal/mdhighlight",
		".chroma { color: var(--text); background-color: var(--code); }",
		"@media (prefers-color-scheme: light)",
		".chroma .k { color:",
		".chroma .nf { color:",
		"@media (prefers-color-scheme: dark)",
		"background-color: #303446",
		"color: #c6d0f5",
		".chroma .c, .chroma .ch, .chroma .cm, .chroma .c1, .chroma .cs, .chroma .cp, .chroma .cpf { color: #949cbb; }",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stylesheet does not contain %q", want)
		}
	}
}

func TestDefaultBindingIsLoopbackAndPortIsManaged(t *testing.T) {
	cfg, err := parseServeFlags(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.bind != "127.0.0.1" {
		t.Errorf("default bind = %q", cfg.bind)
	}
	if cfg.port != 0 {
		t.Errorf("default port = %d, want 0", cfg.port)
	}
	if cfg.portSet {
		t.Error("default port was marked explicit")
	}
	if cfg.root != "." {
		t.Errorf("default root = %q", cfg.root)
	}
}

func TestFlagsMayAppearBeforeOrAfterPath(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"before path", []string{"--port", "8642", "--bind", "127.0.0.2", "./plans"}},
		{"after path", []string{"./plans", "--port", "8642", "--bind", "127.0.0.2"}},
		{"around path", []string{"--bind", "127.0.0.2", "./plans", "--port", "8642"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseServeFlags(tt.args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.root != "./plans" || cfg.port != 8642 || cfg.bind != "127.0.0.2" {
				t.Errorf("parseServeFlags() = %#v", cfg)
			}
		})
	}
}

func mustRead(t *testing.T, filename string) string {
	t.Helper()
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func entryNames(entries []navEntry) string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return strings.Join(names, ",")
}

// errorsAs keeps the main test body readable without making routeError expose
// implementation details solely for tests.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
