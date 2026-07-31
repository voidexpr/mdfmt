# Feature: generic text file viewer

## Goal

Expand `mdfmt serve` from a Markdown-only documentation browser into a safe
viewer for the regular files beneath its configured root:

- Markdown files continue to render as formatted documents through Goldmark.
- Files whose names Chroma recognizes render as syntax-highlighted source.
- Other text files render as escaped plain text in a monospace block.
- Directory pages and the left sidebar show all visible files, grouped by
  extension.

The feature remains a local, read-oriented browser. It must not weaken the
existing path-token, hidden-file, symlink, traversal, or content-security
protections.

## Scope

The initial implementation affects:

- `mdfmt serve`, which lists and previews generic files;
- `mdfmt open`, which accepts generic regular files exposed by `serve`;
- directory and document navigation shared by those commands.

The following behavior remains Markdown-specific:

- `mdfmt save` continues to produce standalone HTML only from Markdown;
- Markdown heading extraction and the table of contents;
- Markdown root-relative link rewriting;
- editor controls, unless generic editing is explicitly added later;
- the proposed static `build` command in `feature_build_site.md`.

Generic files do not become Markdown merely because Chroma has a Markdown
lexer. `.md` and `.markdown` files keep using the existing Goldmark rendering
pipeline.

## File discovery and filesystem boundaries

Directory discovery should include every non-hidden regular file that resolves
inside the served root. Preserve the existing rules:

- omit names beginning with `.`;
- reject a resolved path containing a hidden component;
- reject symlinks that escape the root;
- reject symlinks whose resolved target is not a regular file or directory;
- reject malformed URL components and traversal attempts;
- never expose an absolute filesystem path in generated HTML.

The current defense that checks both the requested filename and the resolved
symlink target's Markdown extension should be replaced with a generic regular
file check. Containment and hidden-component checks still apply to both the
requested path and resolved target.

Directories remain listed separately before file groups. Dotfiles such as
`.env` remain unavailable even though they are often text configuration files.
Changing hidden-file policy is a separate security decision.

## Chroma matching

Do not maintain a hand-written list of extensions. Chroma describes lexers
with complete filename globs, not only extensions. Examples include ordinary
extensions, compound names such as `*.html.erb`, and extensionless or special
names such as `Dockerfile`, `Makefile`, and `Caddyfile*`.

When a file is opened:

1. Handle Markdown through Goldmark.
2. Verify that the content is previewable text and within the size limit.
3. Call Chroma's filename matcher with `filepath.Base(filename)`.
4. If the selected lexer is not Chroma's fallback lexer, tokenize and format
   the file as highlighted source.
5. If Chroma selects its fallback lexer, render escaped plain text in the same
   source-code frame without language-specific token spans.

Chroma's matcher scans the lexer registry and is documented as relatively
slow, but it only needs to run when a file is opened. Directory listing and
extension grouping do not require a Chroma lookup. Avoid prematurely building
an incomplete extension index. If measurement shows a cost, cache the selected
lexer by filename pattern or normalized basename in the server.

Refactor `internal/mdhighlight` so lexer registration and class-based Chroma
formatting can be shared by:

- fenced code blocks in Markdown;
- whole-file source previews;
- the custom Qeylan lexer.

Use the existing generated `assets/syntax.css`. Whole-file formatting must use
the same `WithClasses(true)` and `WithAllClasses(true)` choices so light and
dark themes continue to work without inline styles.

## Text, binary, and size handling

Listing all regular files does not mean reading arbitrary content into memory.
Before rendering a preview:

- inspect `fs.FileInfo.Size()` before `os.ReadFile`;
- enforce a named maximum preview size for Markdown, highlighted source, and
  plain text;
- reject NUL-containing content as binary;
- require valid UTF-8 for an initial implementation;
- never pass binary or oversized input to Goldmark, Chroma, or `html/template`.

A proposed initial preview limit is 8 MiB. Keep it as a named constant so it
can be adjusted or exposed as a CLI option later. A file beyond the limit
should return a normal metadata page explaining that the preview is too large,
not attempt a partial syntax rendering. A binary or invalid-UTF-8 file should
similarly show that no text preview is available.

These informational pages may still show the filename, modification time,
size, breadcrumbs, and sibling navigation. They must not embed any file bytes.

The initial generic viewer does not need a binary download endpoint. If one is
added later, it should use `Content-Disposition: attachment`,
`application/octet-stream`, `X-Content-Type-Options: nosniff`, and the same
root/path-token validation.

## Rendering and content safety

Source files must always be displayed as source. In particular, opening an
`.html`, `.svg`, `.xml`, or `.js` file must never cause its contents to execute
or become part of the page DOM.

- Chroma output may be marked `template.HTML` only after it has passed through
  Chroma's escaping formatter.
- Plain text must be escaped with `html/template` or `html.EscapeString` before
  it is marked safe for the page template.
- Do not serve a source file with its native executable MIME type.
- Retain the current restrictive Content Security Policy, `nosniff`, frame,
  and referrer headers.
- Preserve path-token-free generated HTML by continuing to use relative page
  URLs beneath tokenized routes.

Add a generic document kind to the page model rather than overloading Markdown
flags implicitly. Suitable kinds are:

```text
directory
markdown
source
plain-text
unavailable-preview
```

Markdown pages retain their current body style, readable title, right-hand
table of contents, Raw action, and optional Edit action. Source and plain-text
pages use the filename as the page title, have no heading TOC, and render a
scrollable monospace source frame. The frame should preserve whitespace and
long lines while allowing horizontal scrolling.

The existing `?raw=1` endpoint may remain available for valid text files and
should be relabeled from `Raw Markdown` to `Raw`. It should respond as
`text/plain; charset=utf-8` with `nosniff`. Binary and oversized files should
not acquire a raw inline response as part of this feature.

## Directory grouping

Group files by their final extension using a case-insensitive key:

```text
No extension
.go
.html
.md
.yaml
```

Use the original extension spelling only for filenames; group labels should be
normalized to lower case. `README`, `Makefile`, and `Dockerfile` belong to the
`No extension` group even when Chroma can highlight them. A compound filename
such as `page.html.erb` belongs to `.erb`; Chroma still receives the full
filename and can choose the more specific lexer.

Order groups with `No extension` first, followed by extensions using the
existing case-insensitive name ordering. Continue sorting files within each
group by filename initially. The saved directory-table sort choice should
apply independently to every group.

Replace the flat `pageData.Files` assumption with a structure similar to:

```text
fileGroup
  key
  label
  files []navEntry
```

Both the main directory view and left navigation use the same ordered groups.
The left sidebar should show a compact group heading followed by its files.
Directories remain a separate navigation section above file groups.

The main table can retain filename, modification time, age, size, and title.
The title cell is populated only for Markdown, where it can still use the
existing title cache; it is empty for generic files. This avoids reading every
file or running Chroma while generating a directory page.

Update the empty-directory message from `No Markdown files or directories
here` to wording that reflects generic files.

## Routing and command behavior

The request handler currently accepts only `.md` and `.markdown` regular files.
Refactor routing into classification followed by rendering:

1. resolve and validate the request beneath the served root;
2. route directories through the existing directory renderer;
3. require an ordinary regular file for a file route;
4. classify Markdown by the existing case-insensitive filename rule;
5. classify all other files as generic preview candidates;
6. choose Markdown, highlighted source, plain text, or unavailable-preview
   rendering.

Canonical directory redirects, URL escaping, token stripping, relative links,
assets, and editor endpoints remain unchanged.

`mdfmt open` should accept any ordinary file that the server can route, not
only Markdown. It must continue to enforce:

- a configured or explicitly supplied root;
- containment within that root;
- hidden-component rejection;
- remembered port and path-token behavior;
- directory slash URLs and escaped filename components.

Update CLI help and diagnostics from `Markdown file` to `file` where the
command now accepts generic files. Do not broaden `save` diagnostics.

## Editor integration

Keep editor actions Markdown-only in the initial implementation. The viewer is
a read-oriented expansion, while editor actions launch an external process and
are a separate capability boundary. Therefore:

- directory rows for generic files have no Edit action;
- generic file pages have no Edit action;
- editor request validation continues to require Markdown;
- Markdown editor behavior and CSRF token handling remain unchanged.

A later feature may deliberately allow editing all previewable text files. It
would need to replace Markdown-specific validation in `editor.go` while
retaining the loopback host, origin, form token, path-token, containment, and
symlink checks.

## Caching and performance

Continue caching extracted Markdown titles by resolved filename, modification
time, and size. Do not extract titles from generic files.

No global extension index is required for the first implementation:

- grouping needs only `filepath.Ext`;
- lexer matching occurs only on an opened file;
- the browser requests one document at a time;
- Chroma's registry already owns the authoritative filename patterns.

If profiling identifies lexer lookup as meaningful, add a bounded or
process-lifetime cache after correctness tests cover exact filenames,
case-sensitive patterns, compound extensions, aliases, and the fallback lexer.

File contents should not be cached initially. Files may change while the
server is running, and the existing behavior deliberately reads current
content on each request.

## Tests

Add regression coverage for at least:

- directory listings include Markdown, highlighted source, unknown text, and
  extensionless files;
- hidden files and hidden directories remain absent;
- symlinks escaping the root remain rejected for generic files;
- symlinks inside the root are allowed only when their target is a regular,
  non-hidden file;
- extension groups are normalized, ordered, and rendered in both directory
  view and sidebar;
- compound extensions group by their final extension while Chroma matches the
  full filename;
- `Dockerfile` or another extensionless known filename receives highlighting;
- a known extension such as `.go` produces Chroma class markup;
- an unknown UTF-8 file produces escaped monospace output without token spans;
- source containing HTML, SVG, script, and event-handler text cannot execute
  and appears escaped in the response;
- binary, invalid-UTF-8, and oversized files produce unavailable-preview pages
  without embedding content;
- generic file pages retain breadcrumbs, metadata, Raw behavior where
  allowed, sibling navigation, assets, and path-token-free HTML;
- missing or incorrect path tokens still return `404` for generic files;
- `HEAD` performs no body write;
- `open` accepts generic visible files and still rejects hidden or outside-root
  paths;
- `save` remains Markdown-only;
- generic files never receive editor controls or pass the edit endpoint;
- directory sorting works for every extension-group table;
- existing Markdown rendering, syntax-highlighted fences, links, TOC, and
  title behavior remain unchanged.

Include focused unit tests for text classification and Chroma fallback
detection, plus request-level tests for routing and HTML safety. Run the full
race-enabled CI suite because the server, lexer registration, title cache, and
any future matcher cache are shared across concurrent requests.

## Documentation

Update `README.md` and command help to describe `serve` as a browser for
Markdown and source/text files. Document:

- Markdown versus source versus plain-text rendering;
- extension grouping;
- hidden-file exclusion;
- binary, encoding, and size limits;
- the fact that serving a broader root exposes more visible filesystem content;
- Markdown-only `save` and editor behavior;
- continued path-token protection.

The release screenshots can remain Markdown-focused, but a later screenshot
showing mixed extension groups would make the new behavior immediately clear.

## Implementation outline

1. Extract shared Chroma lexer registration and whole-file formatting helpers.
2. Add file classification, UTF-8/binary checks, and the preview-size boundary.
3. Generalize file routing and add source/plain/unavailable page rendering.
4. Change directory listing data to extension groups and update templates,
   CSS, and multi-table sorting JavaScript.
5. Generalize `mdfmt open` while retaining root and token validation.
6. Keep `save` and editor validation Markdown-only and add explicit regression
   tests for those boundaries.
7. Update documentation and run the complete CI and vulnerability checks.

The renderer itself is small. Most of the work is the directory/sidebar data
model, safe handling of arbitrary file contents, and preserving the existing
security invariants. A release-quality implementation should fit in roughly
one focused day, or two if the unavailable-preview and grouped-directory UI
receive additional polish.
