# Feature: materialized Markdown site builds

## Goal

Add a third `mdfmt` command that materializes a directory tree as a static
documentation site:

```text
mdfmt build SOURCE_DIR -o|--output TARGET_DIR [--strict] [--strict-no-dir-index] [-q|--quiet]
```

`serve` remains the just-in-time directory browser. `save` remains the
single-file standalone exporter. `build` recursively creates the static
equivalent of the served site so the result can be copied, synchronized, or
deployed by an external tool.

Deployment versioning, timestamped releases, opaque URLs, symlink activation,
retention, and rollback are deliberately outside `mdfmt`. `TARGET_DIR` is a
staging artifact owned by `mdfmt`; another script or hosting platform decides
how to publish it.

## Confirmed CLI behavior

```text
mdfmt build SOURCE_DIR -o TARGET_DIR
```

- Exactly one source directory is required.
- `-o` / `--output` is required and must name a directory or a path that can
  be created as a directory.
- Flags may appear before or after `SOURCE_DIR`.
- `--hash` is not accepted by `build`.
- On success, print the absolute target directory followed by a newline.
- `-q` / `--quiet` suppresses only the success output.
- Errors and non-strict warnings go to stderr.
- A build starts no server and performs no source-tree writes.

## Build and publication flow

Every invocation uses this private work directory:

```text
$TMPDIR/mdfmt/build
```

The flow is intentionally simple:

1. Resolve and validate `SOURCE_DIR`, `TARGET_DIR`, and the temporary build
   root before deleting anything.
2. Acquire an exclusive build lock outside the directory being cleaned so two
   `mdfmt build` processes cannot race over the shared work tree.
3. Delete all existing content below `$TMPDIR/mdfmt/build`.
4. Recreate the work directory with private permissions.
5. Inventory the source tree and validate routes, links, referenced images,
   and output collisions.
6. Generate the entire site in the work directory.
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
the source path, user name, host name, or other private filesystem details.

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

- a source that is not a readable directory;
- a target that is `/`, the user's home directory, or the current filesystem
  root;
- a target path that is itself a symlink;
- a target equal to, inside, or containing the source directory;
- a target equal to, inside, or containing the temporary build root;
- a source equal to or containing the temporary build root;
- similarly prefixed siblings being mistaken for containment;
- hidden or malformed path tricks that bypass component-based containment.

The `$TMPDIR/mdfmt` directory must itself be a private, non-symlink directory,
and the `build` child must not be a symlink before its contents are cleaned.

Deletion must enumerate validated direct children and remove those exact
paths. It must never call recursive deletion on an unresolved environment
variable, filesystem root, home directory, source root, or target parent.

## Static route mapping

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

`.md` and `.markdown` matching remains case-insensitive.

All generated navigation, breadcrumbs, document links, image links, CSS, and
JavaScript references use relative URLs. The completed tree must work:

- at a web-server root;
- beneath an arbitrary URL prefix;
- inside a timestamped or opaque deployment directory;
- when opened directly from the filesystem where browser behavior permits.

URL components use escaped filesystem names, not invented slugs.

## `index.md` directory landing pages

An `index.md` file, matched case-insensitively, is the authored landing page
for its containing directory:

```text
SOURCE_DIR/plans/index.md        TARGET_DIR/plans/index.html
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
- treat a leading `/` as relative to `SOURCE_DIR`, not the machine root;
- correctly separate URL paths, query strings, and fragments;
- URL-decode before filesystem resolution and URL-escape rewritten output;
- reject or warn about paths outside the resolved source root;
- map local Markdown targets through the finalized output-route manifest;
- map directory targets to their `index.html` route;
- preserve heading fragments;
- leave non-Markdown local links unavailable unless they are supported assets.

Broken local document links are warnings by default and errors with
`--strict`. No generated page may contain an absolute source filesystem path.

## Referenced images

Detect standard Markdown images through Goldmark's AST, including
reference-style image destinations. Raw HTML remains disabled, so images
inside raw HTML are not copied or emitted.

For each image:

- leave remote and `data:` destinations unchanged, subject to the generated
  CSP;
- resolve local relative paths from the containing Markdown document;
- treat root-relative paths as relative to `SOURCE_DIR`;
- reject traversal or symlink resolution outside `SOURCE_DIR`;
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
build pipeline is introduced.

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

Diagnostics name source-relative paths only. They must not be written into
generated HTML or the `.mdfmt` target marker.

## Tests

Use `httptest` where shared HTTP behavior is relevant and temporary
directories for all build tests. Tests require no internet access.

Cover at least:

- build flag parsing, required source, and required output;
- flags before and after the source directory;
- output path printing and `--quiet`;
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
- query and fragment preservation;
- external link preservation;
- broken, escaping, encoded-traversal, and symlink-escaping link diagnostics;
- inline and reference-style image discovery;
- image copying, content deduplication, and URL rewriting;
- remote and data image behavior;
- missing, escaping, hidden, and symlink-escaping image diagnostics;
- raw HTML and raw-HTML images not being emitted;
- shared local CSS and JavaScript assets with no CDN references;
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
