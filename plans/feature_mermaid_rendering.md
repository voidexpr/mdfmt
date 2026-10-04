# Feature: render Mermaid code blocks

## Goal

A fenced code block whose info string is `mermaid` renders as a diagram
instead of a highlighted listing, in `serve`, `build` and `save` output, in
both themes, with no network access. For example:

    ```mermaid
    flowchart TB
        subgraph Browser
            Shell["Trusted Sandstorm shell<br/>Sharing and Powerbox UI"]
            UI["App UI in separate session origin<br/>Sees displayed plaintext"]
            Shell --- UI
        end
        subgraph Host["Trusted server and operating system"]
            Platform["Sandstorm platform<br/>Authentication, access checks"]
            Platform --- Supervisor["Per-grain supervisor<br/>Linux isolation"]
        end
        Shell <--> Platform
        UI -.->|"Remote images permitted by inspected CSP"| External[External server]
    ```

## What was measured

Mermaid has no server-side implementation outside a browser, so the
evaluation centres on running the official library in the page. Measured
with the single-file UMD bundle of mermaid 11.17.2 (MIT licence):

| Item | Value |
| --- | --- |
| Bundle size | 3.6 MB, 0.98 MB gzipped |
| Binary growth when embedded | 18.5 MB to about 22 MB |
| Render time for the example above | 30 to 70 ms after the script is parsed |
| Rendering under the current CSP | broken: 133 blocked inline styles, unstyled boxes, fallback font |
| Rendering with `style-src 'unsafe-inline'` | correct in light and dark themes |

The rendered SVG contains one `<style>` element and about fifty `style`
attributes written by d3. Inline style attributes cannot be allowed by hash
or nonce, so the only way to render in the page is to allow inline styles on
that page. This is the one real trade-off of the feature; the rest is
plumbing.

Labels use `foreignObject` HTML by default, which is what makes `<br/>` in
the example work. Mermaid's strict security level, the default, runs every
label through its bundled DOMPurify and disables click callbacks, so a
diagram cannot inject script even on a page that allows inline styles.

## What the CSP problem is, in plain terms

The Content Security Policy is a list of rules the page hands to the
browser about where scripts and styles may come from. It never requires
anything from Mermaid's own site: the library is vendored and served from
mdfmt's origin, which the current rules already allow. Mermaid also sends
nothing to any backend; parsing, layout and drawing happen entirely in the
browser.

The difficulty is how Mermaid applies its styling. While drawing, it writes
a `<style>` element with CSS text generated at run time and about fifty
`style="fill:…;stroke:…"` attributes on the SVG shapes. The browser calls
these inline styles. The rule `style-src 'self'` means "styles may only
come from CSS files on our origin" and rejects inline styles whoever wrote
them, because the browser cannot tell a trusted script from an injected
one. Our own stylesheet cannot replace them, since the attribute values are
chosen per shape at run time, and a CSP hash can allow a fixed `<style>`
text but not attributes.

The rule only buys something when an attacker can get content into the
page. In mdfmt the content is the reader's own Markdown, raw HTML is
sanitised, and CSS alone cannot send data out while images, fonts and
fetches stay restricted to the origin. That is why allowing inline styles
on diagram pages is a small relaxation rather than a hole.

## Options

1. **Client-side with the vendored library.** Recommended, described below.
   Works everywhere mdfmt output is viewed, follows the theme, keeps the
   source editable, and needs no tooling beyond the binary. The costs are
   the inline-style relaxation on diagram pages and trusting a 3.6 MB
   bundle, which is pinned, vendored and served from mdfmt's own origin.
2. **Server-side rendering.** Keeps the strict policy and needs no script
   in the page, but every variant needs a JavaScript runtime at render
   time. Analysed in the next section.
3. **A remote renderer such as Kroki or mermaid.ink.** Sends document
   content to a third party and breaks offline reading. Rejected.
4. **A Go renderer for a flowchart subset.** Weeks of work for a fraction of
   the syntax. Rejected.

## Server-side rendering in more detail

### What a pre-rendered diagram would be

The output is an SVG file written next to the page, light and dark
variants, referenced with `<img>`. An SVG loaded as an image is its own
document: its inline styles are not governed by the page's policy, it
cannot run script, and it cannot load anything external. The page's
`style-src 'self'` stays as it is, no bundle is downloaded, and the page
works without JavaScript. `app.js` swaps the light and dark file when the
theme toggle is used, and the two files are listed in the site index so the
offline cache picks them up with the page.

Pre-generating two or three sizes is not needed. An SVG scales, and Mermaid
sets `max-width` on its output so it shrinks to the column. What changes
between a phone and a desktop is not the diagram's size but its direction:
a top-to-bottom flowchart is tall on a phone and a left-to-right one is
wide. Direction is authored in the source, and rewriting it automatically
changes the author's intent, so the client has nothing useful to pick from.
One rendering per theme is the right number. The same holds for an
on-demand `serve` endpoint: a request for "the best dimensions" would carry
no information the server can act on.

### What runtime it takes

- **mermaid-cli.** The reference tool runs the real library in a headless
  Chromium through Puppeteer. The dependency is a browser, not just Node:
  several hundred megabytes, slow to start, and hard to sandbox because
  Chromium itself expects network and file access. Output matches the
  browser exactly. Reasonable as an optional `build` step on a machine that
  already has it; wrong for `serve` on demand.
- **Browser-free renderers.** Newer projects re-implement Mermaid's layout
  without a DOM and ship pre-measured font metrics, so they run under Node,
  Deno or Bun with no browser. The one to evaluate is `beautiful-mermaid`,
  which at the time of writing covers flowcharts, sequence, state, class and
  entity-relationship diagrams; coverage must be checked against the
  current release and against the diagram types actually used. Output
  differs slightly from the official library, which is acceptable for
  documentation.
- **Sandboxing Node or Deno.** A renderer that reads source on stdin and
  writes SVG on stdout needs no other capability. Deno can express that
  directly: run with no network, no file and no environment permissions,
  one process per render, a timeout, and a cap on output size. Node's
  permission model is newer and less complete. In `serve` the result is
  cached by source hash and theme, so the process cost is paid once per
  diagram; in `build` each diagram renders once; `save` renders inline. If
  the runtime is missing, the renderer falls back to the client-side path
  or to showing the source.
- **Embedding the renderer in the binary.** Because a browser-free renderer
  has no DOM dependency, it may run inside a JavaScript engine embedded in
  Go, such as goja. That would give server-side SVG with no external
  dependency at all, in every mode, with the strict policy intact. Whether
  the renderer's code runs under goja, and how fast, needs a spike before
  it can be planned.

### Assessment

Server-side rendering is the better end state on security grounds and the
only path that keeps documents free of script. Its cost is a second
rendering engine with partial syntax coverage and, unless the embedded
spike succeeds, an external runtime that `serve` must find and sandbox.
The client-side path is complete today, covers every diagram type, and its
risks are small in mdfmt's setting: the library is pinned, nothing it could
reach is secret, and the relaxation is confined to diagram pages. The plan
therefore implements the client-side path and keeps the server-side path
as a follow-up, gated on the embedded-engine spike.

## Design

### Markdown pipeline

A small goldmark extension in `internal/mdmermaid`, modelled on the existing
`internal/mdhighlight`:

- an AST transformer replaces every fenced code block with info string
  `mermaid` by a `MermaidBlock` node, so the highlighter never sees it;
- the node renders as `<pre class="mermaid">` containing the escaped
  source, which is also what the page shows if the script fails to load;
- the transformer records in the parser context that the document has at
  least one diagram, and `renderedDocument` gains `HasMermaid`, read the
  same way `HasH1` is, so pages without diagrams stay exactly as they are.

The raw view, the editor and the table of contents are untouched. The
`go.abhg.dev/goldmark/mermaid` extension does the same transformation and
was considered; the in-tree version is about sixty lines and gives the
`HasMermaid` flag directly, so it is preferred.

### The library as an asset

`assets/mermaid.min.js` is checked in, pinned to one version, with a
Makefile target that downloads it from npm and verifies a recorded SHA-256
so an upgrade is a one-line change plus a review of the diff in behaviour.
It is embedded like the other assets and served at `.mdfmt/mermaid.js` in
`serve` and written to `_mdfmt/mermaid.js` by `build`, with a `?v=` version
like the stylesheet. `THIRD_PARTY_NOTICES.md` gains Mermaid's MIT notice;
the bundle includes d3, dagre, DOMPurify and others under their own
MIT-style licences, which the notice lists as part of Mermaid.

### Loading only where needed

A page whose document has a diagram includes
`<script src="…/mermaid.js" defer>` after `app.js`. Nothing else changes
for other pages, so the 3.6 MB is fetched and parsed only when a diagram is
on screen. `app.js` already runs on `DOMContentLoaded`, which fires after
deferred scripts, so it finds `window.mermaid` when present and otherwise
leaves the `<pre>` blocks alone.

### Rendering in the page

For each `pre.mermaid`, `app.js` calls `mermaid.render` with the source
text and inserts the returned SVG into a `div.diagram` placed after the
`<pre>`, which is then hidden. The source stays in the document, so the
diagram can be re-rendered at any time:

- **Theme.** Mermaid is initialised with its `default` or `dark` theme to
  match the effective page theme, the site's font stack, and
  `securityLevel: "strict"`. The existing theme toggle triggers a
  re-render of every diagram, which takes tens of milliseconds each.
- **Errors.** A diagram that fails to parse keeps its `<pre>` visible and
  gets a short `.diagram-error` line with Mermaid's message, instead of the
  library's error graphic. Editing then saving the file in `serve` reloads
  the page and re-renders.
- **Size.** The SVG is centred, limited to the column width, and a diagram
  wider than the column scrolls horizontally inside its container, the way
  tables do. Backgrounds stay transparent so the page theme shows through.

### Content Security Policy

Pages with a diagram get `style-src 'self' 'unsafe-inline'`; every other
page keeps `style-src 'self'`. The relaxation is applied per page in all
three modes:

- `serve` sets the header per response, so `setSecurityHeaders` takes the
  flag from the rendered document;
- `build` writes the policy into each page's `<meta>` tag;
- `save` computes it per file, as it already does for the hashed inline
  stylesheet and script.

Why this is acceptable: inline styles cannot execute script, and the
directives that would turn CSS into an exfiltration channel stay closed
(`img-src 'self' data:`, `connect-src 'self'`, `font-src` falls back to
`default-src 'none'`). The content is the reader's own Markdown, sanitised by
Mermaid before it reaches the DOM. The alternative, rendering inside an
iframe pointing at a separate same-origin host page with its own policy,
would keep the document's policy strict but costs a height-reporting
protocol, theme forwarding, printing quirks and a second page to cache
offline. Not worth it for this threat model; noted here in case the
decision is revisited.

### Sandboxing the library with SES

Hardened JavaScript (the `ses` library) runs code in a compartment with
frozen built-ins and only the globals it is handed. It isolates code whose
interface is small and pure. Mermaid's is not: it needs `document` to build
the SVG, to measure text for layout and for its DOMPurify sanitiser. Handing
the compartment `document` hands it the page, because `document.defaultView`
leads back to `window`, and with it to storage, fetch and navigation. A
membrane that exposes only the DOM calls Mermaid and d3 need is a large,
fragile project, and every library upgrade would break it.

It also protects against the wrong thing. A diagram is not JavaScript; a
malicious diagram can only exploit a bug in Mermaid, and the strict
security level with DOMPurify is the defence for that. A compromised
Mermaid bundle would arrive through an upgrade, and the defence is the
pinned checksum and reviewing the diff. If isolation from a compromised
bundle were required, the effective tool is a sandboxed iframe with an
opaque origin, not SES, and the server-side path removes the question
entirely. The plan uses neither: there is no user data on these pages
beyond the documents, which the library has to receive to draw them.

### Standalone `save` output

A saved file whose document has a diagram inlines the bundle in a second
`<script>` whose hash joins the policy, exactly like `app.js`. The file
grows by 3.6 MB, which is the price of a self-contained document; files
without diagrams are unchanged. If that proves too heavy for routine use,
a `--no-diagrams` flag that leaves the `<pre>` source in place is a small
addition, left out of this plan.

### Offline reading

The worker's shell precache does not include the bundle, so a site without
diagrams never downloads it. Visiting a diagram page caches it through the
normal network-first path. For the folder cache button, the site index
marks pages that have a diagram, and the button adds the bundle to its
targets when any page in scope is marked, so a folder cached for offline
reading renders its diagrams offline. The automatic sync plan
(`feature_ios_offline_sync.md`) inherits this through the same index flag.

## Files touched

- `internal/mdmermaid/mermaid.go` and test: the extension.
- `server.go`: register the extension in `newGoldmark`; `HasMermaid` on
  `renderedDocument` and `pageData`; the asset route; the per-response CSP.
- `build.go`: write the asset; per-page CSP; `mermaid` flag in the site
  index.
- `save.go`: inline the bundle and its hash when the document has a
  diagram.
- `templates/page.html` and `templates/standalone.html`: the conditional
  script tag.
- `assets/app.js`: render, theme re-render, error display.
- `assets/style.css`: `.diagram`, `.diagram-error`.
- `assets/mermaid.min.js`, `Makefile`, `THIRD_PARTY_NOTICES.md`,
  `README.md`.

## Implementation order

1. The extension and the `HasMermaid` flag, with the `<pre class="mermaid">`
   output. Visible result: the source is shown verbatim instead of being
   highlighted as an unknown language.
2. Vendoring, the asset routes, the conditional script tag and the
   rendering in `app.js`, with the CSP change in `serve`. This delivers the
   feature for `serve`.
3. `build` and `save`, including their CSP handling.
4. Theme re-render and the error display.
5. The site index flag and the folder cache button.
6. Notices, README, tests.

## Tests

Go tests:

- a `mermaid` fence renders as `<pre class="mermaid">` with escaped source
  and no highlight markup; other fences are unchanged;
- `HasMermaid` is set only when such a fence exists;
- `serve` responds with `'unsafe-inline'` in `style-src` only for a
  document with a diagram, and the script tag appears only there;
- `build` output: the asset exists under `_mdfmt/`, the diagram page's
  `<meta>` policy and script tag differ from a page without one, the site
  index marks the page;
- `save`: the bundle and its hash are present only with a diagram;
- the asset route serves `text/javascript` and requires the path token.

Browser checks with playwright-cli against `serve`:

- the example renders as an SVG with styled nodes in both themes, and
  toggling the theme re-renders;
- a syntax error shows the source and the message;
- a page without diagrams reports no CSP violations and loads no bundle;
- the folder cache includes the bundle and the diagram renders offline.

## Non-goals

- Server-side or build-time SVG generation in this iteration; see the
  server-side section and the embedded-engine spike in the open questions.
- Mermaid's `click` callbacks, interactive zoom, or exporting diagrams.
- Custom per-site Mermaid configuration; the theme mapping and font are
  fixed.

## Open questions

- Whether to run the embedded-engine spike before or after shipping the
  client-side path. It is a day's experiment: load `beautiful-mermaid` into
  goja, render the example, and measure. A success would change the
  recommended end state to server-side SVG.
- Which Mermaid line to pin at implementation time. 11.17.2 was measured;
  12.x is current and should behave the same, but its bundle should be
  measured before pinning.
- Whether `save` should inline the bundle by default. The plan says yes for
  self-containment; a flag can follow if saved files become unwieldy.
