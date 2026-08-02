package main

import (
	"bytes"
	"html"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
	xhtml "golang.org/x/net/html"
)

const sanitizedRawHTMLAttribute = "mdfmt-sanitized-raw-html"

type safeRawHTMLExtension struct{}

func (safeRawHTMLExtension) Extend(markdown goldmark.Markdown) {
	markdown.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(safeRawHTMLRenderer{}, 100),
	))
}

type safeRawHTMLRenderer struct{}

func (safeRawHTMLRenderer) RegisterFuncs(registry renderer.NodeRendererFuncRegisterer) {
	registry.Register(ast.KindRawHTML, renderSafeRawHTML)
	registry.Register(ast.KindHTMLBlock, renderSafeRawHTML)
}

func renderSafeRawHTML(
	writer util.BufWriter,
	_ []byte,
	node ast.Node,
	entering bool,
) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	value, ok := node.AttributeString(sanitizedRawHTMLAttribute)
	if !ok {
		return ast.WalkSkipChildren, nil
	}
	sanitized, ok := value.([]byte)
	if !ok {
		return ast.WalkSkipChildren, nil
	}
	_, _ = writer.Write(sanitized)
	return ast.WalkSkipChildren, nil
}

func annotateSafeRawHTMLWithRewriter(
	node ast.Node,
	source []byte,
	rewriter markdownDestinationRewriter,
) {
	raw, ok := rawHTMLSource(node, source)
	if !ok {
		return
	}
	sanitized, ok := sanitizeRawHTMLWithRewriter(raw, rewriter)
	if ok && len(sanitized) > 0 {
		node.SetAttributeString(sanitizedRawHTMLAttribute, sanitized)
	}
}

func rawHTMLSource(node ast.Node, source []byte) ([]byte, bool) {
	var output bytes.Buffer
	switch node := node.(type) {
	case *ast.RawHTML:
		for i := range node.Segments.Len() {
			segment := node.Segments.At(i)
			_, _ = output.Write(segment.Value(source))
		}
	case *ast.HTMLBlock:
		for i := range node.Lines().Len() {
			line := node.Lines().At(i)
			_, _ = output.Write(line.Value(source))
		}
		if node.HasClosure() {
			_, _ = output.Write(node.ClosureLine.Value(source))
		}
	default:
		return nil, false
	}
	return output.Bytes(), true
}

// sanitizeRawHTML accepts only anchors, images, and text contained by an HTML
// block. It reserializes every accepted token so browser parsing never sees the
// original, untrusted tag or attribute spelling.
func sanitizeRawHTML(
	raw []byte,
	directoryComponents []string,
	rewriteRootLinks bool,
) ([]byte, bool) {
	var rewriter markdownDestinationRewriter
	if rewriteRootLinks {
		rewriter = func(destination []byte, _ bool) []byte {
			return rewriteRootRelativeDestination(destination, directoryComponents)
		}
	}
	return sanitizeRawHTMLWithRewriter(raw, rewriter)
}

func sanitizeRawHTMLWithRewriter(raw []byte, rewriter markdownDestinationRewriter) ([]byte, bool) {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(raw))
	var output bytes.Buffer
	for {
		switch tokenType := tokenizer.Next(); tokenType {
		case xhtml.ErrorToken:
			if tokenizer.Err() == io.EOF {
				return output.Bytes(), true
			}
			return nil, false
		case xhtml.TextToken:
			_, _ = output.WriteString(html.EscapeString(string(tokenizer.Text())))
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			tag := strings.ToLower(token.Data)
			attributes, ok := sanitizedAttributes(tag, token.Attr, rewriter)
			if !ok {
				return nil, false
			}
			switch tag {
			case "a":
				if tokenType == xhtml.SelfClosingTagToken {
					return nil, false
				}
				_, _ = output.WriteString("<a")
			case "img":
				_, _ = output.WriteString("<img")
			default:
				return nil, false
			}
			writeSanitizedAttributes(&output, attributes)
			_ = output.WriteByte('>')
		case xhtml.EndTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "a") || len(token.Attr) != 0 {
				return nil, false
			}
			_, _ = output.WriteString("</a>")
		case xhtml.CommentToken, xhtml.DoctypeToken:
			return nil, false
		default:
			return nil, false
		}
	}
}

type safeHTMLAttribute struct {
	name  string
	value string
}

func sanitizedAttributes(
	tag string,
	attributes []xhtml.Attribute,
	rewriter markdownDestinationRewriter,
) ([]safeHTMLAttribute, bool) {
	var allowed map[string]bool
	var order []string
	var requiredURL string
	switch tag {
	case "a":
		allowed = map[string]bool{"href": true, "title": true}
		order = []string{"href", "title"}
		requiredURL = "href"
	case "img":
		allowed = map[string]bool{
			"src": true, "alt": true, "title": true, "width": true, "height": true,
		}
		order = []string{"src", "alt", "title", "width", "height"}
		requiredURL = "src"
	default:
		return nil, false
	}

	values := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		name := strings.ToLower(attribute.Key)
		if attribute.Namespace != "" || !allowed[name] {
			continue
		}
		if _, duplicate := values[name]; duplicate {
			return nil, false
		}
		value := attribute.Val
		switch name {
		case "href", "src":
			var ok bool
			value, ok = sanitizedRawHTMLURL(value, tag == "img", rewriter)
			if !ok {
				return nil, false
			}
		case "width", "height":
			if !validImageDimension(value) {
				continue
			}
		}
		values[name] = value
	}
	if _, ok := values[requiredURL]; !ok {
		return nil, false
	}

	result := make([]safeHTMLAttribute, 0, len(values))
	for _, name := range order {
		if value, ok := values[name]; ok {
			result = append(result, safeHTMLAttribute{name: name, value: value})
		}
	}
	return result, true
}

func sanitizedRawHTMLURL(
	raw string,
	image bool,
	rewriter markdownDestinationRewriter,
) (string, bool) {
	if raw == "" && image {
		return "", false
	}
	if strings.TrimSpace(raw) != raw || strings.ContainsRune(raw, '\x00') {
		return "", false
	}
	target, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(target.Scheme) {
	case "", "http", "https":
	case "mailto":
		if image {
			return "", false
		}
	default:
		return "", false
	}
	if rewriter != nil && target.Scheme == "" && target.Host == "" {
		raw = string(rewriter([]byte(raw), image))
	}
	return raw, true
}

func validImageDimension(raw string) bool {
	value, err := strconv.ParseUint(raw, 10, 16)
	return err == nil && value > 0 && value <= 10_000
}

func writeSanitizedAttributes(output *bytes.Buffer, attributes []safeHTMLAttribute) {
	for _, attribute := range attributes {
		_ = output.WriteByte(' ')
		_, _ = output.WriteString(attribute.name)
		_, _ = output.WriteString(`="`)
		_, _ = output.WriteString(html.EscapeString(attribute.value))
		_ = output.WriteByte('"')
	}
}
