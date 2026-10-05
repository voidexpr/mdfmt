// Package mdmermaid turns fenced ```mermaid code blocks into diagram
// containers that the page script renders with the Mermaid library.
package mdmermaid

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Block is a Mermaid diagram source, replacing the fenced code block so the
// syntax highlighter never sees it.
type Block struct {
	ast.BaseBlock
}

// Kind identifies Block nodes.
var Kind = ast.NewNodeKind("MermaidBlock")

// Kind implements ast.Node.
func (*Block) Kind() ast.NodeKind { return Kind }

// Dump implements ast.Node.
func (b *Block) Dump(source []byte, level int) { ast.DumpHelper(b, source, level, nil, nil) }

// IsRaw marks the source as verbatim text, like a code block.
func (*Block) IsRaw() bool { return true }

// Extension returns the Goldmark extension shared by serve, build and save.
func Extension() goldmark.Extender { return extension{} }

type extension struct{}

func (extension) Extend(markdown goldmark.Markdown) {
	markdown.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(transformer{}, 100)))
	markdown.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(nodeRenderer{}, 100)))
}

type transformer struct{}

func (transformer) Transform(document *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var fences []*ast.FencedCodeBlock
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if fence, ok := node.(*ast.FencedCodeBlock); ok && entering && bytes.EqualFold(fence.Language(source), []byte("mermaid")) {
			fences = append(fences, fence)
		}
		return ast.WalkContinue, nil
	})
	for _, fence := range fences {
		block := &Block{}
		block.SetLines(fence.Lines())
		fence.Parent().ReplaceChild(fence.Parent(), fence, block)
	}
}

type nodeRenderer struct{}

func (nodeRenderer) RegisterFuncs(registry renderer.NodeRendererFuncRegisterer) {
	registry.Register(Kind, render)
}

func render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</pre>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre class="mermaid">`)
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
	return ast.WalkContinue, nil
}
