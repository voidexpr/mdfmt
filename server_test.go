package main

import (
	"bytes"
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

func TestRelativeURL(t *testing.T) {
	tests := []struct {
		name      string
		base      []string
		target    []string
		directory bool
		want      string
	}{
		{name: "root from nested", base: []string{"foo", "bar"}, directory: true, want: "../../"},
		{name: "sibling file", base: []string{"foo", "bar"}, target: []string{"foo", "bar", "next.md"}, want: "next.md"},
		{name: "parent directory", base: []string{"foo", "bar"}, target: []string{"foo"}, directory: true, want: "../"},
		{name: "escaped target", base: []string{"foo"}, target: []string{"a b", "Guide #1.md"}, want: "../a%20b/Guide%20%231.md"},
		{name: "current directory", base: []string{"foo"}, target: []string{"foo"}, directory: true, want: "./"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := relativeURL(test.base, test.target, test.directory); got != test.want {
				t.Errorf("relativeURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestServedMarkdownRewritesRootRelativeLinks(t *testing.T) {
	source := []byte(strings.Join([]string{
		"[root](/guide.md?view=full#install)",
		"[home](/)",
		"[relative](next.md)",
		"[external](https://example.com/guide.md)",
		"[network](//example.com/guide.md)",
		"![image](/images/example.png)",
	}, "\n\n"))
	rendered, err := renderMarkdownDocumentForServe(
		newGoldmark(),
		source,
		"current.md",
		[]string{"foo", "bar"},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	body := string(rendered.Body)
	for _, want := range []string{
		`href="../../guide.md?view=full#install"`,
		`href="../../"`,
		`href="next.md"`,
		`href="https://example.com/guide.md"`,
		`href="//example.com/guide.md"`,
		`src="../../images/example.png"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered body does not contain %q:\n%s", want, body)
		}
	}

	standalone, err := renderMarkdownDocument(newGoldmark(), []byte("[root](/guide.md)\n"), "current.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(standalone.Body), `href="/guide.md"`) {
		t.Errorf("ordinary rendering rewrote a root-relative link:\n%s", standalone.Body)
	}
}

func TestPathTokenRoutingAndHTMLPrivacy(t *testing.T) {
	const token = "secret-path-token-123"
	root := t.TempDir()
	writeTestFile(t, root, "root.md", "# Root\n")
	writeTestFile(t, root, "foo/bar/guide.md", "# Guide\n\n[Root](/root.md)\n")
	writeTestFile(t, root, "foo/bar/next.md", "# Next\n")
	server := testServer(t, root)
	server.pathToken = token
	server.editor = &editorLauncher{token: "editor-request-token"}

	for _, target := range []string{"/", "/root.md", "/wrong/root.md", "/.mdfmt/style.css"} {
		response := request(t, server, target)
		if response.Code != http.StatusNotFound {
			t.Errorf("unprefixed/wrong route %q status = %d, want 404", target, response.Code)
		}
	}

	rootRedirect := request(t, server, "/"+token)
	if rootRedirect.Code != http.StatusMovedPermanently || rootRedirect.Header().Get("Location") != "/"+token+"/" {
		t.Errorf("token root redirect: status=%d location=%q", rootRedirect.Code, rootRedirect.Header().Get("Location"))
	}
	directoryRedirect := request(t, server, "/"+token+"/foo/bar")
	if directoryRedirect.Code != http.StatusMovedPermanently || directoryRedirect.Header().Get("Location") != "/"+token+"/foo/bar/" {
		t.Errorf("directory redirect: status=%d location=%q", directoryRedirect.Code, directoryRedirect.Header().Get("Location"))
	}

	document := request(t, server, "/"+token+"/foo/bar/guide.md")
	if document.Code != http.StatusOK {
		t.Fatalf("document status = %d; body=%s", document.Code, document.Body.String())
	}
	body := document.Body.String()
	if strings.Contains(body, token) {
		t.Fatalf("document HTML leaked the path token:\n%s", body)
	}
	for _, want := range []string{
		`href="../../.mdfmt/style.css?v=7"`,
		`src="../../.mdfmt/app.js?v=6"`,
		`href="../../.mdfmt/favicon.svg" type="image/svg+xml" sizes="any"`,
		`href="../../.mdfmt/apple-touch-icon.png" sizes="180x180"`,
		`href="../../root.md"`,
		`href="next.md"`,
		`href="?raw=1"`,
		`action="../../.mdfmt/edit"`,
		`name="return" value="/foo/bar/guide.md"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("document HTML does not contain %q:\n%s", want, body)
		}
	}

	directory := request(t, server, "/"+token+"/foo/bar/")
	if directory.Code != http.StatusOK {
		t.Fatalf("directory status = %d; body=%s", directory.Code, directory.Body.String())
	}
	if strings.Contains(directory.Body.String(), token) {
		t.Fatalf("directory HTML leaked the path token:\n%s", directory.Body.String())
	}

	raw := request(t, server, "/"+token+"/foo/bar/guide.md?raw=1")
	if raw.Code != http.StatusOK || !strings.Contains(raw.Body.String(), "[Root](/root.md)") {
		t.Errorf("prefixed raw response: status=%d body=%s", raw.Code, raw.Body.String())
	}
	asset := request(t, server, "/"+token+"/.mdfmt/style.css?v=7")
	if asset.Code != http.StatusOK {
		t.Errorf("prefixed asset status = %d", asset.Code)
	}
	favicon := request(t, server, "/"+token+"/.mdfmt/favicon.svg")
	if favicon.Code != http.StatusOK || favicon.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("prefixed favicon: status=%d content-type=%q", favicon.Code, favicon.Header().Get("Content-Type"))
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

func TestIndexMarkdownIsDirectoryLandingPage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "plans"), "index.md", "# Project Plans\n\n## Next\n")
	writeTestFile(t, filepath.Join(root, "plans"), "other.md", "# Other\n")
	server := testServer(t, root)

	landing := request(t, server, "/plans/")
	if landing.Code != http.StatusOK {
		t.Fatalf("landing status = %d", landing.Code)
	}
	body := landing.Body.String()
	for _, want := range []string{`<h1 id="project-plans">Project Plans</h1>`, `href="#next"`, `href="/plans/other.md"`} {
		if !strings.Contains(body, want) {
			t.Errorf("landing does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `>index</a>`) || strings.Contains(body, `index.md</a>`) {
		t.Errorf("landing lists its own index.md:\n%s", body)
	}

	redirect := request(t, server, "/plans/index.md")
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "/plans/" {
		t.Errorf("direct index response = %d Location %q", redirect.Code, redirect.Header().Get("Location"))
	}
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
	writeTestFile(t, root, "real.png", "image bytes")
	writeTestFile(t, root, "plain.txt", "non-markdown secret\n")
	writeTestFile(t, root, ".internal.md", "# Hidden Target\n")
	writeTestFile(t, sibling, "secret.md", "# Sibling Secret\n")
	writeTestFile(t, sibling, "secret.png", "sibling image secret")
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
	if err := os.Symlink(filepath.Join(root, "real.png"), filepath.Join(root, "inside.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "secret.png"), filepath.Join(root, "escape.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "plain.txt"), filepath.Join(root, "disguised.png")); err != nil {
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
	if response := request(t, server, "/inside.png"); response.Code != http.StatusOK {
		t.Errorf("in-root image symlink status = %d, want 200", response.Code)
	}
	if response := request(t, server, "/escape.png"); response.Code != http.StatusForbidden {
		t.Errorf("escaping image symlink status = %d, want 403", response.Code)
	}
	if response := request(t, server, "/disguised.png"); response.Code != http.StatusNotFound {
		t.Errorf("image alias to non-image target status = %d, want 404", response.Code)
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

func TestSafeRawAnchorAndImageHTML(t *testing.T) {
	root := t.TempDir()
	imageBytes := []byte("not-a-real-png-but-served-as-opaque-image-bytes")
	imagePath := filepath.Join(root, "docs", "file.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, imageBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "docs.png"), []byte("directory image"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "README.md", `# Screenshots

<a href="./docs/file.png" title="Full image" onclick="bad()"><img src="./docs/file.png" alt="Preview &amp; more" width="480" onerror="bad()"></a>
<a href="./docs/docs.png"><img src="./docs/docs.png" alt="Directory preview" width="480"></a>

<script>alert(1)</script>

<div>raw layout</div>
`)
	writeTestFile(t, root, "docs/notes.txt", "not exposed")
	writeTestFile(t, root, "docs/unsafe.svg", `<svg onload="bad()"></svg>`)

	server := testServer(t, root)
	document := request(t, server, "/README.md")
	if document.Code != http.StatusOK {
		t.Fatalf("document status = %d; body=%s", document.Code, document.Body.String())
	}
	body := document.Body.String()
	for _, want := range []string{
		`<a href="./docs/file.png" title="Full image"><img src="./docs/file.png" alt="Preview &amp; more" width="480"></a>`,
		`<a href="./docs/docs.png"><img src="./docs/docs.png" alt="Directory preview" width="480"></a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("document does not contain sanitized screenshot markup %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"onclick=", "onerror=", "<script>alert(1)</script>", "<div>raw layout</div>", "raw HTML omitted"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("document contains forbidden value %q:\n%s", forbidden, body)
		}
	}

	image := request(t, server, "/docs/file.png")
	if image.Code != http.StatusOK {
		t.Fatalf("image status = %d; body=%s", image.Code, image.Body.String())
	}
	if got := image.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("image Content-Type = %q, want image/png", got)
	}
	if !bytes.Equal(image.Body.Bytes(), imageBytes) {
		t.Errorf("image body = %q, want %q", image.Body.Bytes(), imageBytes)
	}

	directory := request(t, server, "/docs/")
	if strings.Contains(directory.Body.String(), "file.png") || strings.Contains(directory.Body.String(), "docs.png") {
		t.Error("image unexpectedly appeared in the Markdown directory listing")
	}
	for _, target := range []string{"/docs/notes.txt", "/docs/unsafe.svg"} {
		if response := request(t, server, target); response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", target, response.Code)
		}
	}
}

func TestSafeRawHTMLUsesTokenFreeRelativeURLs(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "guide.md", "# Root guide\n")
	writeTestFile(t, root, "plans/nested/page.md", `<a href="/guide.md?view=full#top"><img src="/images/preview.webp" alt="Preview"></a>`)
	imagePath := filepath.Join(root, "images", "preview.webp")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := testServer(t, root)
	server.pathToken = "secret-token"
	document := request(t, server, "/secret-token/plans/nested/page.md")
	if document.Code != http.StatusOK {
		t.Fatalf("document status = %d; body=%s", document.Code, document.Body.String())
	}
	body := document.Body.String()
	for _, want := range []string{
		`href="../../guide.md?view=full#top"`,
		`src="../../images/preview.webp"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("document does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "secret-token") {
		t.Fatalf("document leaked path token:\n%s", body)
	}
	if response := request(t, server, "/images/preview.webp"); response.Code != http.StatusNotFound {
		t.Errorf("unprefixed image status = %d, want 404", response.Code)
	}
	if response := request(t, server, "/secret-token/images/preview.webp"); response.Code != http.StatusOK {
		t.Errorf("prefixed image status = %d, want 200", response.Code)
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

func TestEmbeddedFaviconAssets(t *testing.T) {
	server := testServer(t, t.TempDir())
	tests := []struct {
		path        string
		contentType string
		content     []byte
	}{
		{"favicon.svg", "image/svg+xml", faviconSVGAsset},
		{"favicon.ico", "image/x-icon", faviconICOAsset},
		{"favicon-16.png", "image/png", favicon16Asset},
		{"favicon-32.png", "image/png", favicon32Asset},
		{"favicon-48.png", "image/png", favicon48Asset},
		{"apple-touch-icon.png", "image/png", appleTouchIconAsset},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := request(t, server, "/.mdfmt/"+test.path)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.Code)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
			if !bytes.Equal(response.Body.Bytes(), test.content) {
				t.Error("response does not contain the embedded favicon")
			}
			if got := response.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", got)
			}
		})
	}
}

func TestServedStylesheetIncludesSyntaxThemes(t *testing.T) {
	root := t.TempDir()
	response := request(t, testServer(t, root), "/.mdfmt/style.css?v=7")
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
	if cfg.pathToken != pathTokenAuto {
		t.Errorf("default path token = %q, want auto", cfg.pathToken)
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
