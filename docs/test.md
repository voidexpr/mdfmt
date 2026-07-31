# mdfmt formatting showcase

This sample document demonstrates how `mdfmt serve` turns ordinary Markdown
into a focused, navigable documentation page.

> **Tip:** edit this file and refresh the browser—the server renders changes
> immediately, without a restart.

## Quick tour

The layout keeps the document tree on the left, the content in the center, and
an automatically generated table of contents on the right.

| Markdown feature | Rendered result |
| --- | --- |
| Headings | Stable anchor links and page navigation |
| Fenced code | Syntax highlighting with light and dark themes |
| Tables and lists | Clean, responsive typography |

## Code highlighting

```go
package main

import "fmt"

func main() {
	fmt.Println("Markdown, formatted.")
}
```

## A practical checklist

- [x] Write Markdown
- [x] Run `mdfmt serve ./docs`
- [x] Browse a polished local documentation site
- [ ] Publish when it is ready

### Nested content

1. Use headings to structure longer documents.
2. Add links, such as the [mdfmt repository](https://github.com/voidexpr/mdfmt).
3. Keep the source portable and readable in any editor.
