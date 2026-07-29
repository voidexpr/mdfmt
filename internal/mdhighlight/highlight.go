// Package mdhighlight configures fenced Markdown code highlighting for mdfmt.
package mdhighlight

import (
	"embed"
	"fmt"
	"html"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/util"
)

//go:generate go run ./cmd/stylegen -output ../../assets/syntax.css

//go:embed qeylan.xml
var lexerFiles embed.FS

var registerLexers sync.Once

// Extension returns the Goldmark extension shared by mdfmt serve and save.
func Extension() goldmark.Extender {
	registerLexers.Do(func() {
		lexers.Register(chroma.MustNewXMLLexer(lexerFiles, "qeylan.xml"))
	})

	return highlighting.NewHighlighting(
		highlighting.WithStyle("catppuccin-latte"),
		highlighting.WithGuessLanguage(false),
		highlighting.WithFormatOptions(
			chromahtml.WithClasses(true),
			// Class markup must work with both the light and dark stylesheets.
			chromahtml.WithAllClasses(true),
		),
		highlighting.WithWrapperRenderer(renderWrapper),
	)
}

func renderWrapper(w util.BufWriter, context highlighting.CodeBlockContext, entering bool) {
	language, hasLanguage := context.Language()
	escapedLanguage := ""
	if hasLanguage {
		escapedLanguage = html.EscapeString(string(language))
	}

	if context.Highlighted() {
		if entering {
			_, _ = w.WriteString(`<div class="highlight"`)
			if hasLanguage {
				_, _ = fmt.Fprintf(w, ` data-language="%s"`, escapedLanguage)
			}
			_ = w.WriteByte('>')
			return
		}
		_, _ = w.WriteString("</div>\n")
		return
	}

	if entering {
		_, _ = w.WriteString("<pre")
		if hasLanguage {
			_, _ = fmt.Fprintf(w, ` data-language="%s"`, escapedLanguage)
		}
		_, _ = w.WriteString("><code")
		if hasLanguage {
			_, _ = fmt.Fprintf(w, ` class="language-%s"`, escapedLanguage)
		}
		_ = w.WriteByte('>')
		return
	}
	_, _ = w.WriteString("</code></pre>\n")
}
