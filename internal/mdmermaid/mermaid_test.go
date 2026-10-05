package mdmermaid

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
)

func TestMermaidFenceBecomesDiagramContainer(t *testing.T) {
	markdown := goldmark.New(goldmark.WithExtensions(Extension()))
	source := "```Mermaid\nflowchart LR\n    A[\"x <br/> y\"] --> B\n```\n\n```go\nfmt.Println()\n```\n"
	var output bytes.Buffer
	if err := markdown.Convert([]byte(source), &output); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	want := "<pre class=\"mermaid\">flowchart LR\n    A[&quot;x &lt;br/&gt; y&quot;] --&gt; B\n</pre>\n"
	if !strings.Contains(html, want) {
		t.Errorf("diagram container missing:\n%s", html)
	}
	if !strings.Contains(html, `<pre><code class="language-go">`) {
		t.Errorf("other fences should stay code blocks:\n%s", html)
	}
}
