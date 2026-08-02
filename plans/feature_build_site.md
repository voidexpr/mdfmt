# Feature: materialized Markdown site builds

## Goal

Add a third `mdfmt` command that materializes one directory tree, or a
collection of disjoint trees, as a static documentation site:

```text
mdfmt build SOURCE_DIR -o|--output TARGET_DIR [--path-token none|auto|VALUE]
    [--strict] [--strict-no-dir-index] [-q|--quiet]

mdfmt build --mount MOUNT_PATH=SOURCE_DIR [--mount MOUNT_PATH=SOURCE_DIR ...]
    -o|--output TARGET_DIR [--path-token none|auto|VALUE]
    [--strict] [--strict-no-dir-index] [-q|--quiet]
```

`serve` remains the just-in-time directory browser. `save` remains the
single-file standalone exporter. `build` recursively creates the static
equivalent of the served site so the result can be copied, synchronized, or
deployed by an external tool.

Deployment versioning, timestamped releases, symlink activation, retention,
and rollback are deliberately outside `mdfmt`. `build` may optionally place
the generated site beneath one opaque URL path component, but authentication,
authorization, and deployment policy remain external. `TARGET_DIR` is a
staging artifact owned by `mdfmt`; another script or hosting platform decides
how to publish it.

## Confirmed CLI behavior

```text
mdfmt build SOURCE_DIR -o TARGET_DIR
```

- The existing single-source form requires exactly one positional source
  directory and preserves its current URL layout directly beneath
  `SITE_ROOT`.
- The collection form requires one or more repeatable `--mount
  MOUNT_PATH=SOURCE_DIR` options and accepts no positional source directory.
- A build cannot mix a positional source with `--mount`.
- `-o` / `--output` is required and must name a directory or a path that can
  be created as a directory.
- Flags may appear before or after `SOURCE_DIR`.
- `--hash` is not accepted by `build`.
- `--path-token` accepts `none`, `auto`, or a validated custom value and
  defaults to `none`.
- On success, print the absolute generated site root followed by a newline.
  This is `TARGET_DIR` with `none` and `TARGET_DIR/TOKEN` otherwise.
- `-q` / `--quiet` suppresses only the success output.
- Errors and non-strict warnings go to stderr.
- A build starts no server and performs no source-tree writes.

## Multiple source roots

The collection form publishes several disjoint source directories as one
site. Each source is assigned an explicit URL mount path:

```text
mdfmt build \
    --mount a="$HOME/projects/a" \
    --mount b="$HOME/projects/b" \
    --mount research="$HOME/projects/c/research" \
    --output "$HOME/public/mdfmt" \
    --path-token auto
```

With an automatic token of `4f8c...`, this produces:

```text
TARGET_DIR/
    .mdfmt
    4f8c.../
        index.html
        _mdfmt/
        a/
            index.html
        b/
            index.html
        research/
            index.html
```

The collection is one owned build artifact. One invocation inventories and
generates every mount and replaces the complete previous collection. Separate
`build` invocations must not cooperate by writing individual projects into the
same `TARGET_DIR`: target cleanup and automatic token rotation would make that
unsafe and leave stale-project handling ambiguous.

`MOUNT_PATH=SOURCE_DIR` is split at the first `=` so a source path may itself
contain `=`. A mount path:

- is a non-empty relative URL path, possibly containing multiple components;
- has no leading or trailing slash, empty component, `.` component, or `..`
  component;
- must not contain the reserved `_mdfmt` component;
- must not equal or contain another configured mount, ignoring case for
  portable output safety;
- is URL-escaped component by component when emitted, without being replaced
  by an invented slug.

Source roots must resolve to distinct, non-overlapping directories. Validate
all mounts and source roots together before inventorying or modifying the
target. Diagnostics identify a source by mount path and a path relative to
that source, never by its absolute filesystem path.

`SITE_ROOT/index.html` is a generated collection hub that lists every mount in
command-line order. The mount path is initially also its display label. Every
page has a collection-home link and a persistent project switcher, while its
ordinary directory navigation remains scoped to the current mount. A later
manifest-file feature may add custom titles, descriptions, icons, and ordering
without complicating the initial CLI.

The single positional source form does not introduce a collection hub or
mount component; it remains the concise choice for publishing one tree.

## Optional URL path token

`build` supports the same option vocabulary as `serve`:

```text
--path-token none
--path-token auto
--path-token SOME_VALUE
```

Its static behavior is deliberately different from the live server:

- `none`, the default, emits the site directly in `TARGET_DIR` and preserves
  the current deterministic URL layout;
- `auto` generates a fresh cryptographically random 128-bit token for that
  invocation and emits the site beneath `TARGET_DIR/TOKEN`;
- a custom value emits beneath `TARGET_DIR/VALUE` and gives deployment tooling
  a stable caller-controlled URL component.

Custom values use the existing path-token validation: 1–128 ASCII letters,
digits, underscores, or hyphens. `none` and `auto` are modes rather than
literal custom tokens. A token is exactly one path component and is never
URL-decoded into separators.

An automatic build token is not cached. Rebuilding with `auto` rotates the
token and removes the previous generated token directory during owned-target
replacement. A collection receives one token shared by its hub and every
mount, never one token per source. Deployments requiring a stable path must
pass a custom value. The token is also not written into the `.mdfmt` target
marker, a second cache, or any generated HTML page.

Define these paths during planning and generation:

```text
TARGET_DIR       owned staging directory containing the .mdfmt marker
SITE_ROOT        TARGET_DIR when token=none, otherwise TARGET_DIR/TOKEN
WORK_SITE_ROOT   corresponding location beneath the private work directory
```

All site content, including `_mdfmt` assets, is emitted beneath `SITE_ROOT`.
When a token is enabled, `TARGET_DIR` itself contains only the ownership marker
and the token directory. Do not generate a root `index.html`, redirect, token
manifest, or directory-listing helper because each would disclose or bypass
the opaque path.

All page-to-page, breadcrumb, image, favicon, CSS, and JavaScript URLs remain
relative to the page. The token therefore appears in the browser address and
output filesystem layout but not in page source. Root-relative Markdown links
and allowlisted raw-HTML `href`/`src` values must be rewritten through the
normal static route manifest rather than emitted with a leading `/`.

Static path tokens are obscurity, not authentication. `mdfmt` cannot prevent a
hosting provider, directory index, access log, browser history, deployment
manifest, or administrator from revealing the directory name. A deployment
using this feature must:

- publish `TARGET_DIR` as the document root so the token remains in the URL;
- disable directory listings at `TARGET_DIR`;
- avoid catch-all or SPA fallbacks that serve the tokenized index for an
  incorrect path;
- retain the generated `no-referrer` policy;
- use real authentication when access control is required.

Publishing only `SITE_ROOT` as the web-server root intentionally removes the
token boundary and is equivalent to `--path-token none` from a visitor's
perspective.

## Build and publication flow

Every invocation uses this private work directory:

```text
$TMPDIR/mdfmt/build
```

The flow is intentionally simple:

1. Resolve and validate every source/mount mapping, `TARGET_DIR`, the
   path-token option, and the temporary build root before deleting anything.
2. Acquire an exclusive build lock outside the directory being cleaned so two
   `mdfmt build` processes cannot race over the shared work tree.
3. Delete all existing content below `$TMPDIR/mdfmt/build`.
4. Recreate the work directory with private permissions.
5. Inventory every source tree and build one composite route manifest,
   validating routes, links, referenced images, and output collisions.
6. Generate the entire site beneath `WORK_SITE_ROOT`, adding the token
   directory only as a physical output prefix and never to page routes.
7. If generation or validation fails, leave `TARGET_DIR` untouched.
8. Validate ownership of `TARGET_DIR`.
9. Create the marker when claiming a missing or empty target, or preserve the
   existing valid marker.
10. Delete every target child except the marker.
11. Copy the completed work tree into `TARGET_DIR`.
12. Remove the temporary work contents and release the lock.

Copying to the target is not an atomic deployment. A copy failure can leave a
partial staging directory, but the `.mdfmt` marker allows the next build to
clear and recreate it. Atomic production deployment and rollback belong to
the external script consuming this staging artifact.

The lock is necessary because the work path is fixed and cleaned at the start
of every build. An interrupted process must not cause a later process to
silently break an active build; stale-lock handling should report a clear
error and the lock path rather than guessing that it is safe to remove.

## Target ownership and deletion safety

The marker is a regular file at:

```text
TARGET_DIR/.mdfmt
```

It contains only a format identifier and schema version. It must not contain
the source path, user name, host name, path token, or other private filesystem
details.

Target rules:

- A missing target directory may be created and claimed.
- An empty target directory may be claimed.
- A non-empty target is accepted only when it contains a valid, regular
  `.mdfmt` marker.
- The marker itself is preserved when rebuilding an owned target.
- Every other target child is removed before copying the new build.
- A symlink, directory, malformed file, or unknown-version `.mdfmt` marker is
  an error.
- There is no `--force` bypass in the initial implementation.

Before cleanup, resolve paths with `filepath.Abs` and `filepath.EvalSymlinks`
and reject:

- any source that is not a readable directory;
- a target that is `/`, the user's home directory, or the current filesystem
  root;
- a target path that is itself a symlink;
- a target equal to, inside, or containing any source directory;
- a target equal to, inside, or containing the temporary build root;
- any source equal to or containing the temporary build root;
- source roots that are equal to, inside, or containing one another;
- similarly prefixed siblings being mistaken for containment;
- hidden or malformed path tricks that bypass component-based containment.

The `$TMPDIR/mdfmt` directory must itself be a private, non-symlink directory,
and the `build` child must not be a symlink before its contents are cleaned.

Deletion must enumerate validated direct children and remove those exact
paths. It must never call recursive deletion on an unresolved environment
variable, filesystem root, home directory, source root, or target parent.

## Static route mapping

Route generation is relative to `SITE_ROOT`; the token is never part of the
logical route manifest. In the single-source form with `--path-token none`:

Directory routes use `index.html`:

```text
SOURCE_DIR/                     TARGET_DIR/index.html
SOURCE_DIR/plans/               TARGET_DIR/plans/index.html
```

Ordinary Markdown filenames lose their Markdown extension:

```text
guide.md                        guide.html
plans/design.markdown           plans/design.html
```

With `--path-token auto` producing `4f8c...` or with that value supplied
explicitly, the same logical routes have a physical prefix:

```text
SOURCE_DIR/                     TARGET_DIR/4f8c.../index.html
SOURCE_DIR/plans/               TARGET_DIR/4f8c.../plans/index.html
guide.md                        TARGET_DIR/4f8c.../guide.html
plans/design.markdown           TARGET_DIR/4f8c.../plans/design.html
```

The route manifest should store paths relative to `SITE_ROOT`, not paths with
the token prepended. Apply the token only when selecting the output directory.
This keeps link rewriting identical in tokenized and unprefixed builds.

In the collection form, prepend each configured mount path to that source's
logical routes and reserve the site root for the generated collection hub:

```text
--mount a=SOURCE_A
SOURCE_A/                       SITE_ROOT/a/index.html
SOURCE_A/guide.md               SITE_ROOT/a/guide.html

--mount notes/research=SOURCE_B
SOURCE_B/                       SITE_ROOT/notes/research/index.html
SOURCE_B/results.md             SITE_ROOT/notes/research/results.html
```

The route manifest records both mount identity and source-relative path. Two
sources cannot collide because their mount paths are validated as disjoint;
collisions within a mount follow the ordinary collision policy. `_mdfmt` is
shared once at `SITE_ROOT`, including when several mounts reference identical
content-addressed media.

`.md` and `.markdown` matching remains case-insensitive.

All generated navigation, breadcrumbs, document links, image links, favicon
links, CSS, and JavaScript references use relative URLs. The completed tree
must work:

- at a web-server root;
- beneath an arbitrary URL prefix;
- inside a timestamped or opaque deployment directory;
- when opened directly from the filesystem where browser behavior permits.

URL components use escaped filesystem names, not invented slugs.

## `index.md` directory landing pages

An `index.md` file, matched case-insensitively, is the authored landing page
for its containing directory:

```text
SOURCE_DIR/plans/index.md        SITE_ROOT/plans/index.html
```

For a collection mount named `a`, the same source file maps beneath the mount:

```text
SOURCE_DIR/plans/index.md        SITE_ROOT/a/plans/index.html
```

This landing-page rule applies to both `build` and `serve`. `serve` checks only
the directory currently requested and therefore remains lazy. A direct served
request for `index.md` redirects to its containing slash-form directory URL so
there is one canonical route.

When `index.md` exists:

- render it through the normal Markdown document pipeline;
- use it instead of the generated directory table;
- retain breadcrumbs, left navigation, right-hand TOC, heading IDs,
  scroll-based highlighting, metadata, theme, and responsive layout;
- omit `index.md` from that directory's ordinary file list because the
  directory link already reaches it;
- keep other Markdown files and child directories in the left navigation.

Only `index.md` has this special role. An `index.markdown` file remains an
ordinary Markdown document unless this rule is expanded deliberately later.
Two case variants of `index.md` in the same logical directory are an output
collision. A static build reports it through the collision policy; `serve`
returns an internal error rather than choosing one nondeterministically.

## Generated directory listings

By default, a directory without `index.md` receives the same generated
directory listing shown by `mdfmt serve`:

- directory name;
- breadcrumbs;
- child directories sorted alphabetically;
- Markdown files sorted alphabetically;
- filename, modification time, age, size, and readable title;
- sortable file table;
- current-directory navigation.

In a collection, generated directory listings and document pages additionally
show the collection-home link and project switcher. They must not merge files
from different mounts into one directory table or left-navigation tree.

This supports the important zero-configuration use case of publishing an
arbitrary collection of Markdown files without requiring authored index
pages.

With:

```text
--strict-no-dir-index
```

every visible directory included in the site must contain an `index.md`.
Missing index files are reported together and fail the build before
`TARGET_DIR` is modified. The flag never silently creates a broken directory
link or an empty landing page.

The generated collection hub is structural and exempt from this requirement.
Each mount root and its visible source directories are still subject to it.

Hidden directories remain excluded. A visible empty directory therefore also
requires `index.md` under this flag; users who do not want it in the site
should remove or hide it.

## Output collision policy

The default route strips `.md` or `.markdown`. Build an output-route manifest
before rendering and detect collisions using both exact names and a portable
case-insensitive comparison.

For example:

```text
guide.md
guide.markdown
```

both initially map to `guide.html`.

In normal mode:

- warn about the collision;
- retain the Markdown extension for every member of that collision group;
- emit `guide.md.html` and `guide.markdown.html`;
- rewrite all generated links through the finalized manifest.

`index.md` colliding with another `index.html` output follows the same rule,
except that its special directory-landing route takes precedence and the
other output is disambiguated.

If retaining the extension still collides, append a short deterministic
suffix derived from the source-relative path and warn.

With `--strict`, any output collision is an error. Strict-mode errors are
collected and reported before rendering or target cleanup.

## Internal Markdown links

Use Goldmark's parsed AST rather than regular expressions to inspect and
rewrite links.

For every Markdown link:

- leave `http`, `https`, `mailto`, protocol-relative, and other explicit
  schemes unchanged;
- resolve relative filesystem links against the source document directory;
- treat a leading `/` as relative to the current source root/mount, not the
  combined collection or machine root;
- correctly separate URL paths, query strings, and fragments;
- URL-decode before filesystem resolution and URL-escape rewritten output;
- reject or warn about paths outside the resolved source root;
- map local Markdown targets through the finalized output-route manifest;
- map directory targets to their `index.html` route;
- preserve heading fragments;
- leave non-Markdown local links unavailable unless they are supported assets.

Broken local document links are warnings by default and errors with
`--strict`. No generated page may contain an absolute source filesystem path.

Relative links may not escape their current configured source root, even when
the resulting filesystem path happens to fall beneath another configured
source. Cross-project navigation is supplied by the generated hub and project
switcher. A future explicit virtual-site link syntax may support authored
cross-mount links without making filesystem traversal or leading `/`
ambiguous.

## Referenced images

Detect standard Markdown images through Goldmark's AST, including
reference-style image destinations. Also inspect the sanitized `<img src>`
subset supported by the shared Markdown renderer; disallowed raw HTML remains
omitted.

For each image:

- leave remote and `data:` destinations unchanged, subject to the generated
  CSP;
- resolve local relative paths from the containing Markdown document;
- treat root-relative paths as relative to the current source root/mount;
- reject traversal or symlink resolution outside that source root;
- require a readable regular source file;
- copy only images referenced by rendered Markdown;
- preserve the original extension when practical;
- place copied files beneath a reserved generated asset directory;
- use content-addressed names to deduplicate identical images and avoid
  source-path disclosure;
- rewrite the rendered image destination to a relative generated URL.

Missing, unreadable, unsupported, or escaping images are warnings by default
and errors with `--strict`. The initial implementation does not resize,
optimize, convert, or interpret image contents.

## Shared static assets

Unlike single-file `save`, a site build should emit the embedded theme assets
once:

```text
_mdfmt/style.css
_mdfmt/app.js
_mdfmt/media/...
```

Pages reference them relatively. No CDN, font host, frontend framework, or
build pipeline is introduced. Emit the embedded favicon variants and Apple
touch icon in the same namespace and include them on every generated page.

The generated CSP permits only the generated local CSS, JavaScript, and image
assets. Include a referrer-policy meta element. Document that HTTP-only
headers such as `X-Frame-Options` and CSP `frame-ancestors` must be configured
by the eventual web server and cannot be guaranteed by static HTML.

The `_mdfmt` namespace is reserved. A source path that would map onto a
generated asset path is a collision and follows the normal strict/non-strict
policy where possible.

## Metadata behavior

Modification timestamp and file size are build-time snapshots.

Relative age text such as `3h ago` must be calculated in the browser from an
embedded machine-readable timestamp so it does not become stale merely
because the static site was not rebuilt. The same script updates ages on both
document breadcrumbs and directory tables.

Directory sorting retains the existing local browser persistence and must
remain consistent across generated directories.

## Shared implementation

Keep a single presentation pipeline for `serve`, `save`, and `build`:

- one safe Goldmark configuration;
- one heading-ID and TOC builder;
- shared title extraction;
- shared breadcrumb and navigation models;
- a build-only collection hub and project-switcher model layered onto the
  shared navigation;
- shared directory sorting and metadata formatting;
- shared document and directory templates where their layouts match;
- shared embedded CSS and JavaScript.

`serve` remains lazy and must not begin recursively inventorying the tree.
`build` performs the recursive inventory because it needs a complete route
manifest and referenced-asset graph before producing static output.

`save` retains its current single-file behavior and does not inherit site link
or image rewriting.

## Diagnostics

Default warnings include:

- output-route collisions that were disambiguated;
- broken local Markdown links;
- missing or unreadable referenced images;
- unsupported local asset links;
- filesystem entries skipped because they cannot be read safely.

`--strict` promotes all build warnings to errors.

`--strict-no-dir-index` is independent: it specifically requires authored
`index.md` landing pages and may be combined with `--strict`.

Diagnostics name source-relative paths only; in a collection they prefix that
path with its mount path to disambiguate sources. They must not be written
into generated HTML or the `.mdfmt` target marker. The success line may
contain the token because it prints `SITE_ROOT`; warnings and errors should
not repeat an automatically generated token unless identifying the output
path is necessary for recovery.

## Tests

Use `httptest` where shared HTTP behavior is relevant and temporary
directories for all build tests. Tests require no internet access.

Cover at least:

- build flag parsing, required positional source or mount, and required
  output;
- flags before and after the source directory;
- the positional single-source form retaining its existing root layout;
- repeatable `--mount` parsing and rejection when mixed with a positional
  source;
- mount values split on the first `=` and clear missing-name/source errors;
- nested mount paths and rejection of leading/trailing slashes, empty, `.`,
  `..`, `_mdfmt`, overlapping, duplicate, and case-colliding mount paths;
- rejection of equal, nested, and symlink-overlapping source roots;
- collection hub ordering, links, and token-free page source;
- persistent collection-home and project-switcher navigation from every
  mount depth;
- mount-scoped directory listings, breadcrumbs, and left navigation;
- a single shared `_mdfmt` asset tree and content-addressed media store across
  mounts;
- whole-collection replacement removing a mount deleted from the next build;
- `--path-token` defaulting to `none`;
- explicit `none`, generated `auto`, valid custom values, reserved modes, and
  invalid custom-token rejection;
- output path printing of `TARGET_DIR` versus `TARGET_DIR/TOKEN` and `--quiet`;
- automatic-token uniqueness across builds and custom-token determinism;
- tokenized physical output layout with no root index or token manifest;
- automatic rebuild removal of the previous token directory;
- an unchanged `.mdfmt` marker that contains no token;
- token-free generated HTML at root and nested depths;
- relative Markdown, sanitized raw-HTML, image, favicon, CSS, and JavaScript
  URLs under a tokenized output directory;
- logical route-collision results remaining identical with and without a
  physical token prefix;
- cleaning the fixed temporary build directory;
- build-lock contention and cleanup;
- empty and missing target claiming;
- valid marker preservation;
- non-empty unmarked target rejection;
- malformed, symlinked, and unknown marker rejection;
- deletion of stale owned output;
- source/target/temp overlap rejection in both directions;
- root, home, traversal, and similarly prefixed sibling safety;
- target untouched after an inventory, validation, or render failure;
- root and nested generated directory pages;
- `index.md` replacing a generated directory listing;
- case-insensitive `index.md` recognition;
- omission of `index.md` from ordinary file navigation;
- default generated listings when `index.md` is absent;
- `--strict-no-dir-index` success and aggregated failures;
- ordinary `.md` and `.markdown` route mapping;
- case-insensitive extension handling;
- normal-mode collision disambiguation and warnings;
- strict collision failure;
- secondary collision fallback;
- relative navigation and breadcrumb URLs at several depths;
- relative and root-relative Markdown link rewriting;
- root-relative links and images resolving within the current mount;
- relative filesystem traversal into another configured source remaining
  rejected;
- query and fragment preservation;
- external link preservation;
- broken, escaping, encoded-traversal, and symlink-escaping link diagnostics;
- inline and reference-style image discovery;
- image copying, content deduplication, and URL rewriting;
- remote and data image behavior;
- missing, escaping, hidden, and symlink-escaping image diagnostics;
- sanitized raw-HTML anchors/images being emitted and rewritten while all
  other raw HTML remains omitted;
- shared local CSS, JavaScript, and favicon assets with no CDN references;
- generated CSP and referrer policy;
- dynamic age markup and script behavior;
- heading IDs, all h1 headings, nested TOC, and scroll highlighting;
- no source absolute path or directory leakage in any generated file;
- existing `serve` laziness, security boundaries, and ordinary file routing
  remaining unchanged;
- served directory landing through `index.md` and canonical redirect from a
  direct `index.md` request;
- existing `save` behavior remaining unchanged.

Run formatting, the complete test suite, race tests, vet, staticcheck,
`go mod tidy -diff`, and `govulncheck`.
