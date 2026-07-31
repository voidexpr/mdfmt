# Feature: serve multiple Markdown roots

## Goal

Allow one `mdfmt serve` process to expose several independent Markdown
directory trees:

```text
mdfmt serve --port 8888 ~/notes ~/projects
```

When multiple roots are provided, `mdfmt` presents a virtual landing page and
mounts each directory beneath its own URL prefix:

```text
/             virtual root listing all served roots
/notes/       contents of ~/notes
/projects/    contents of ~/projects
```

Each configured directory remains an independent filesystem security boundary.
Serving two roots must not turn their common parent into a readable root or
allow a symlink in one root to access the other.

## Proposed CLI behavior

Change the command form to:

```text
mdfmt serve [OPTIONS] [PATH ...]
```

- With no path, continue serving the current directory.
- With one path, preserve the existing behavior and URL layout exactly. The
  supplied directory remains mounted directly at `/`.
- With two or more paths, serve a virtual root at `/` and mount each supplied
  directory beneath a name derived from its directory basename.
- Flags remain accepted before, between, or after positional paths.
- Resolve and validate every path before opening the listener.
- Every path must resolve to an existing directory.
- Reject the invocation if two paths resolve to the same directory.
- Reject duplicate mount names, including case-only duplicates.
- Preserve the existing editor-selection and loopback-bind rules.

For example:

```text
mdfmt serve --port 8888 ~/notes ~/projects
mdfmt serve ~/notes --port 8888 ~/projects
```

both produce:

```text
/notes/
/projects/
```

Explicit mount aliases such as `--root work=~/projects` are outside the initial
implementation. A duplicate or unusable basename should produce a clear
startup error rather than inventing an unstable suffix.

## Root naming and validation

Represent each validated root with a structure containing at least:

```text
name            URL mount and display name
path            cleaned, absolute, symlink-resolved filesystem path
```

Use `.qy`-style component validation rather than concatenating untrusted route
strings. A mount name must:

- be a single non-empty URL path component;
- not be `.` or `..`;
- not begin with `.`;
- not contain `/`, `\`, or NUL;
- not collide with the reserved `/.mdfmt/` namespace;
- be unique under a case-insensitive comparison.

Derive the initial mount name from the validated directory basename. If a path
does not yield a usable name, fail with a diagnostic naming that path. Do not
expose a larger parent directory merely to obtain a convenient route.

Resolve all roots before checking duplicates so symlink aliases of the same
directory are rejected. Overlapping roots may be allowed when they are
different resolved directories, but each mount still enforces its own
boundary.

## URL routing

Assets and editor requests retain their global routes:

```text
/.mdfmt/style.css
/.mdfmt/app.js
/.mdfmt/edit
```

For a single root, route resolution remains unchanged:

```text
/guide.md
/plans/
```

For multiple roots:

1. `/` renders the virtual root page.
2. The first URL component selects an exact configured mount.
3. Remaining components resolve relative to that mount's filesystem path.
4. `/notes` redirects canonically to `/notes/`.
5. An unknown mount returns `404`.
6. Files cannot be requested directly at `/`; every document route belongs to
   a configured mount.

Keep URL components and filesystem-relative components separate. Existing
code often uses one `components` slice for both purposes; in multi-root mode
the mount component belongs in generated URLs but must not be joined onto the
filesystem root.

Introduce a root-aware URL helper rather than manually prepending mount names
at individual call sites. It must be used for:

- directory and document links;
- redirects to slash-form directory routes;
- breadcrumbs;
- parent navigation;
- document `TopURL`;
- Raw links;
- edit paths and edit return URLs.

## Virtual root page and navigation

The virtual `/` page lists the configured roots as directories sorted with the
existing case-insensitive ordering:

```text
notes/
projects/
```

Reuse the existing page and directory-list presentation where practical. The
page must not display absolute source paths. A suitable title and directory
label is `Roots`.

Multi-root breadcrumbs should make the mount boundary visible:

```text
Roots / notes / plans / guide.md
```

- `Roots` links to `/`.
- `notes` links to `/notes/`.
- A mounted root's parent navigation links back to `/`.
- Navigation below the mounted root behaves as it does today.

Single-root breadcrumbs and parent links must remain byte-for-byte compatible
where existing tests assert them.

## Server data model

Replace the single `serveConfig.root` value with a path slice:

```text
serveConfig.roots []string
```

The server should own a validated ordered collection and lookup map:

```text
markdownServer.roots       ordered roots for the virtual page
markdownServer.rootsByName exact route dispatch
markdownServer.multiRoot   whether mount prefixes are active
```

A route-selection helper should return:

```text
selected served root
filesystem-relative components
URL components or mount prefix
```

All filesystem operations then receive the selected root explicitly. Avoid
temporarily changing a global `server.root` field while handling a request;
requests are concurrent.

The title cache may remain shared because it is keyed by resolved absolute
filename. Goldmark and embedded assets also remain shared.

## Filesystem isolation

Apply the existing containment and hidden-component checks relative to the
selected root, never relative to:

- the common ancestor of all roots;
- the process working directory;
- another configured root;
- the unresolved command-line path.

For every request:

1. Select the mount from the URL.
2. Join only the remaining validated components to that root.
3. resolve symlinks;
4. verify the result is still within the selected root;
5. reject hidden resolved components;
6. apply the existing Markdown-file and regular-file checks.

A symlink from `~/notes/link.md` to `~/projects/file.md` remains rejected even
though `~/projects` is also configured. Access to that file must go through
its `/projects/` route.

Do not implement multiple roots by serving their common parent. That would
expose unconfigured siblings and weaken the current filesystem boundary.

## Directory listings and documents

Make directory listing helpers root-aware:

- `listDirectory` validates entries against the selected root;
- title extraction continues using the resolved filename;
- generated entry URLs include the selected mount prefix;
- document navigation lists only siblings in the same mounted root;
- Raw responses use the same selected-root resolution;
- modification time, age, size, and sorting behavior remain unchanged.

No cross-root document merging, global search, or automatic cross-root
navigation is included. The virtual root page is the only shared navigation
layer.

## Editor integration

Editor support currently stores one allowed root and uses it both for
validation and as the editor process working directory. Refactor it so the
server resolves an edit path to:

```text
selected root
resolved Markdown filename
```

Then:

- validate the edit URL through the same multi-root route selection as GET
  requests;
- verify the file remains within the selected root;
- pass the selected root into the launcher;
- set `command.Dir` to that selected root;
- retain one executable, argument list, token, host, and origin policy for the
  whole server;
- keep edit form paths mounted, such as `/notes/guide.md`;
- reject a forged edit path that crosses into another root or omits its mount.

The editor launcher should not accept a file merely because it falls within
any configured root after an earlier route selected a different root.

## Logging and diagnostics

For one root, preserve the existing startup log format.

For multiple roots, log the listening address and each mount mapping clearly,
for example:

```text
serving multiple roots at http://127.0.0.1:8888
serving /notes/ from /Users/example/notes
serving /projects/ from /Users/example/projects
```

Startup errors should identify:

- a missing or non-directory path;
- duplicate resolved roots;
- duplicate mount names;
- invalid or reserved mount names.

HTTP errors should continue avoiding absolute filesystem paths in response
bodies. Existing server-side logs may retain resolved paths where useful for
local diagnostics.

## Backward compatibility

The following must remain unchanged for zero or one path:

- default root of `.`;
- document and directory URLs;
- redirects;
- breadcrumbs;
- Raw links;
- edit form paths;
- editor working directory;
- security headers;
- hidden-file and symlink behavior;
- command help flags and flag interspersing;
- graceful shutdown and startup logging.

`save` is unaffected. The planned `build` command remains a single-source-tree
operation unless a separate feature deliberately expands it.

## Documentation

Update:

- root and `serve` usage text;
- the README command synopsis;
- the Serve option and behavior sections;
- examples for zero, one, and multiple roots;
- the virtual URL layout;
- duplicate-name behavior;
- the fact that roots remain isolated even when both are configured;
- editor behavior under mounted roots.

## Implementation sequence

1. Change flag parsing and `serveConfig` to collect zero or more paths.
2. Add validated `servedRoot` construction, mount-name checks, and duplicate
   detection.
3. Add route selection for single-root and multi-root modes.
4. Make filesystem resolution and directory listing explicitly root-aware.
5. Centralize root-aware URL generation.
6. Add the virtual root page, breadcrumbs, and parent navigation.
7. Refactor edit-path resolution and editor launching for the selected root.
8. Update logging, help, and README documentation.
9. Run the complete unit, integration, race, static-analysis, generation,
   module-tidiness, and vulnerability checks.

## Tests

Use temporary directories and `httptest`; tests require no internet access.

Cover at least:

- no path still defaults to `.`;
- one path preserves existing parsing and routes;
- two or more positional paths parse with flags before, between, and after
  them;
- every root is resolved and validated before listening;
- missing and non-directory roots fail startup;
- duplicate canonical roots fail startup;
- exact and case-only mount-name collisions fail startup;
- invalid and reserved mount names fail startup;
- `/` renders only the configured virtual roots;
- root entries are sorted and contain no absolute source paths;
- `/notes` redirects to `/notes/`;
- unknown mounts return `404`;
- documents, Raw responses, and nested directories work below each mount;
- all generated document, directory, breadcrumb, parent, and Raw URLs retain
  the mount prefix;
- mounted-root parent navigation returns to `/`;
- sibling navigation does not cross roots;
- traversal attempts remain rejected;
- hidden paths remain unavailable within every root;
- an in-root symlink remains allowed under the existing rules;
- a symlink escaping to an unconfigured directory is rejected;
- a symlink from one configured root into another configured root is also
  rejected;
- similarly prefixed roots do not pass containment checks;
- assets remain available at the global `/.mdfmt/` routes;
- edit forms contain mounted paths and return URLs;
- valid edits launch with the selected root as `command.Dir`;
- forged, cross-root, missing-mount, hidden, and symlink-escaping edit paths
  are rejected;
- loopback host, origin, token, method, and content-type edit protections
  remain unchanged;
- concurrent requests to different roots do not share mutable route state;
- title caching remains correct for equal filenames in different roots;
- single-root HTML and editor tests continue passing without changed URLs;
- complete test and race suites pass.

## Expected scope

The change should require no new dependencies. Expect a moderate refactor of
CLI parsing, route resolution, URL construction, navigation, and editor
validation, plus focused integration tests. A reasonable implementation is
approximately 250–400 lines of production and test changes, depending on how
much existing URL-generation code can be centralized.

## Non-goals

The initial feature does not include:

- explicit mount aliases;
- automatic suffixes for duplicate basenames;
- serving the common parent of configured roots;
- allowing symlinks to cross between configured roots;
- global search or a merged directory tree;
- cross-root Markdown link rewriting;
- per-root editor commands;
- multiple listeners or one port per root;
- changes to `save` or the planned `build` command.
