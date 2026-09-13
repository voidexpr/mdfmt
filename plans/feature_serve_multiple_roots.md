# Feature: serve multiple Markdown roots as mounts

## Goal

Allow one `mdfmt serve` process to expose several independent Markdown
directory trees, using the same mount model `build` already has:

```text
mdfmt serve --port 8888 --mount notes=~/notes --mount projects=~/projects
```

With mounts, `serve` presents the generated Projects page and mounts each
directory beneath its own URL prefix:

```text
/             Projects page listing the mounts
/notes/       contents of ~/notes
/projects/    contents of ~/projects
```

The original reason for hesitating over this feature was that serving a
parent directory achieves the same URLs. That is the wrong trade for a
deployed `serve`: behind a reverse proxy such as Caddy, serving a parent
exposes every sibling beneath it, while mounts expose exactly the chosen
trees. Mounts make `serve` a restricted, deployable equivalent of `build`,
with live rendering instead of a materialized site, and let a build and a
served instance of the same mounts share one URL layout.

Each configured directory remains an independent filesystem security
boundary. Serving two roots must not turn their common parent into a
readable root or allow a symlink in one root to reach the other.

## Confirmed CLI behavior

```text
mdfmt serve [OPTIONS] [PATH]
mdfmt serve --mount MOUNT=SOURCE_DIR [--mount ...] [OPTIONS]
```

- With no path and no mount, continue serving the current directory.
- With one positional path, preserve the existing behavior and URL layout
  exactly. The directory remains mounted directly at `/`.
- With one or more `--mount MOUNT=SOURCE_DIR` options, serve the Projects
  page at `/` and each directory beneath its mount path. A single `--mount`
  still produces the Projects layout, so a deployment can grow from one
  mount to several without changing URLs.
- A positional path cannot be combined with `--mount`; the error text
  mirrors `build`.
- Flags remain accepted before or after the positional path.
- `--bind`, `--port`, `--path-token`, and the editor flags keep their
  meaning. With a token, mounts sit beneath it: `/TOKEN/notes/`.
- Resolve and validate every mount before opening the listener. Every
  source must resolve to an existing directory.

## Mount naming and validation

Reuse the `build` mount parsing and validation rather than the earlier
basename-derived scheme:

- split each `--mount` value on the first `=`, with clear errors for a
  missing name or source;
- accept nested mount paths such as `work/research`, validated component by
  component as `validateBuildMountPath` does;
- reject `.`, `..`, empty components, backslashes, NUL, leading or trailing
  slashes, and `//`;
- reserve the served asset namespace `.mdfmt`; components starting with `.`
  are already unavailable in `serve`, so the check is the same hidden-name
  rule applied to mount paths;
- reject duplicate, case-colliding, and prefix-overlapping mount paths;
- reject equal, nested, and symlink-overlapping source roots.

Extract the mount-list validation from `build.go` into a shared helper that
takes the reserved asset directory name (`_mdfmt` for `build`, `.mdfmt` for
`serve`) so both commands share one implementation and one set of error
messages. `buildMount` keeps its inventory fields; `serve` needs only the
name, components, and resolved root, so a small shared `mountSpec` type
carrying those three is the natural result.

## URL routing

Assets and editor requests retain their global routes beneath the token:

```text
/.mdfmt/style.css
/.mdfmt/app.js
/.mdfmt/manifest.webmanifest
/.mdfmt/edit
```

For a single root, route resolution remains unchanged.

For mounts:

1. `/` renders the Projects page.
2. The longest matching mount path prefix, compared case-insensitively as in
   `build`, selects the mount. Nested mount paths make a one-component
   match insufficient.
3. Remaining components resolve relative to that mount's filesystem root.
4. `/notes` redirects canonically to `/notes/`.
5. A path that matches no mount returns `404`. Files cannot be requested
   directly at `/`; every document route belongs to a mount.

Keep URL components and filesystem-relative components separate. Existing
code uses one `components` slice for both purposes; with mounts the mount
components belong in generated URLs but must never be joined onto the
filesystem root.

Introduce one root-aware URL helper, the served counterpart of
`staticDirectoryURL`, rather than prepending mount names at call sites. It
must be used for directory and document links, slash-form redirects,
breadcrumbs, parent navigation, `TopURL`, Raw links, edit paths and edit
return URLs, and `RootURL`.

Root-relative Markdown links such as `/guide.md` resolve within the current
mount, exactly as `build` treats a leading `/`. The rewrite already exists for
the path-token case; it gains the mount prefix.

## Projects page and navigation

Reuse the `build` presentation instead of a new virtual root page:

- `/` renders the same page `writeCollectionHub` generates: title and
  directory label `Projects`, one directory entry per mount in
  configuration order, no source paths.
- Every page sets `pageData.Projects` from a served equivalent of
  `projectEntries`, so the existing project navigation in the left sidebar
  appears with the current mount marked active.
- Breadcrumbs make the mount boundary visible: `Projects / notes / plans /
  guide.md`, with `Projects` linking to `/` and `notes` to `/notes/`.
- A mount's root directory page links its parent navigation back to `/`,
  labelled as in `build` (`All projects`).
- Navigation below a mount behaves as it does today.

Single-root breadcrumbs and parent links must remain byte-for-byte compatible
where existing tests assert them.

## Reading positions and the home-screen manifest

No template, script, or manifest change is needed. Pages keep announcing
their root page in `data-root`, so document identities simply gain the
mount prefix (`notes/guide.md`), the recent-documents menu works across
mounts, and a home-screen launch resumes the last document in any mount. The
manifest start URL still resolves to `/` or `/TOKEN/`, now the Projects
page.

## Server data model

Replace the single root in `serveConfig` and `markdownServer`:

```text
serveConfig.root     string      positional form
serveConfig.mounts   []string    --mount values, mutually exclusive
markdownServer.mounts []*mountSpec   ordered, nil for the single-root form
```

A route-selection helper returns the selected mount, the filesystem-relative
components, and the URL prefix components. In the single-root form it returns
an implicit mount with no prefix, so the rest of the server has one code
path. All filesystem operations receive the selected root explicitly; never
mutate a shared field while handling a request, since requests are
concurrent.

The title cache may remain shared because it is keyed by resolved absolute
filename. Goldmark and embedded assets also remain shared.

## Filesystem isolation

Apply the existing containment and hidden-component checks relative to the
selected mount root, never relative to the common ancestor of all roots, the
process working directory, another configured root, or the unresolved
command-line path.

For every request:

1. select the mount from the URL;
2. join only the remaining validated components to that root;
3. resolve symlinks;
4. verify the result is still within the selected root;
5. reject hidden resolved components;
6. apply the existing Markdown-file and regular-file checks.

A symlink from `~/notes/link.md` to `~/projects/file.md` remains rejected
even though `~/projects` is also configured. Access to that file must go
through its `/projects/` route. Do not implement mounts by serving their
common parent.

## Directory listings and documents

Make the listing helpers mount-aware: `listDirectory` validates entries
against the selected root, title extraction continues using the resolved
filename, generated entry URLs include the mount prefix, document navigation
lists only siblings in the same mount, Raw responses use the same resolution,
and modification time, age, size, and sorting behavior remain unchanged.

No cross-mount document merging, global search, or automatic cross-mount
navigation is included. The Projects page and project sidebar are the only
shared navigation layer.

## Port registry and `open`

`open` finds the longest registered root containing a path and builds the
URL from that root's port and token. A mounted server has several roots on
one port, so the registry records one entry per mount source, each carrying
the mount path:

```json
{
  "version": 1,
  "roots": {
    "/Users/example/notes":    {"port": 8888, "path_token": "…", "mount": "notes"},
    "/Users/example/projects": {"port": 8888, "path_token": "…", "mount": "projects"}
  }
}
```

- `serve` writes every mount's entry on a successful start, all with the
  bound port and token; the single-root form writes no `mount` field, so
  existing registries and readers are unaffected.
- `open` inserts the mount path between the token and the relative path.
- `config` prints one line per entry as today; the mount is visible in the
  root column ordering and may be shown as a fourth column.
- Port remembering keys on the first mount's source in configuration order,
  so a server started with the same mounts reuses its port; a changed first
  mount is a new association. The remembered-port rules for occupied and
  explicit ports are unchanged.

## Editor integration

Editor support currently stores one allowed root and uses it both for
validation and as the editor process working directory. Refactor it so the
server resolves an edit path to the selected mount and the resolved Markdown
filename, then:

- validate the edit URL through the same route selection as GET requests;
- verify the file remains within the selected root;
- pass the selected root into the launcher and set `command.Dir` to it;
- retain one executable, argument list, token, host, and origin policy for
  the whole server;
- keep edit form paths mounted, such as `/notes/guide.md`;
- reject a forged edit path that crosses into another mount or omits its
  mount.

## Logging and diagnostics

For one root, preserve the existing startup log format. For mounts, log the
listening address and each mapping:

```text
serving projects at http://127.0.0.1:8888/TOKEN/
serving /notes/ from /Users/example/notes
serving /projects/ from /Users/example/projects
```

Startup errors identify a missing or non-directory source, duplicate or
overlapping sources, and invalid, duplicate, or reserved mount paths, using
the shared validation messages. HTTP error bodies continue to avoid absolute
filesystem paths.

## Backward compatibility

The following must remain unchanged for the positional form: default root of
`.`, document and directory URLs, redirects, breadcrumbs, Raw links, edit form
paths, editor working directory, security headers, hidden-file and symlink
behavior, command help flags and flag interspersing, registry entries,
graceful shutdown, and startup logging. `save` and `build` output are
unaffected; `build` only loses its private copy of the mount validation.

## Documentation

Update the `serve` usage text, the README command synopsis, the Serve option
table and behavior sections with mount examples, the URL layout, the port
registry format with the `mount` field, `open` behavior for mounted roots,
the note that mounts stay isolated from each other, editor behavior under
mounts, and a deployment note that mounts are the way to restrict a `serve`
instance published behind a reverse proxy.

## Implementation sequence

1. Extract the mount parsing and validation from `build.go` into a shared
   helper with a reserved-name parameter; keep `build` tests green.
2. Add `--mount` to `serve` flag parsing, mutually exclusive with the
   positional path.
3. Add route selection returning the selected mount, filesystem components,
   and URL prefix, with the single-root form as an implicit mount.
4. Make filesystem resolution and directory listing mount-aware.
5. Centralize mount-aware URL generation, including `RootURL` and the
   root-relative link rewrite.
6. Render the Projects page, project sidebar, breadcrumbs, and parent
   navigation from the `build` presentation.
7. Refactor edit-path resolution and editor launching for the selected mount.
8. Extend the port registry with per-mount entries and teach `open` and
   `config` about the mount field.
9. Update logging, help, and README documentation.
10. Run the complete `make ci` suite.

## Tests

Use temporary directories and `httptest`; tests require no internet access.

Cover at least:

- no path still defaults to `.`;
- one path preserves existing parsing and routes;
- `--mount` parsing with flags before and after, rejection when mixed with a
  positional path, and the shared validation errors for malformed, reserved,
  duplicate, case-colliding, nested-overlapping, and source-overlapping
  mounts;
- every source is resolved and validated before listening; missing and
  non-directory sources fail startup;
- `/` renders the Projects page with only the configured mounts, in order,
  with no source paths;
- the project sidebar on every page with the active mount marked;
- `/notes` redirects to `/notes/`; unknown mounts return `404`;
- nested mount paths route by longest prefix;
- documents, Raw responses, and nested directories work below each mount;
- all generated document, directory, breadcrumb, parent, Raw, edit, and
  `data-root` URLs retain the mount prefix, with and without a path token;
- root-relative Markdown links resolve within the current mount;
- a mount's root directory parent navigation returns to `/`;
- sibling navigation does not cross mounts;
- traversal attempts and hidden paths remain rejected within every mount;
- an in-mount symlink remains allowed; a symlink escaping to an unconfigured
  directory or into another configured mount is rejected;
- similarly prefixed sources do not pass containment checks;
- assets and the manifest remain available at the global `/.mdfmt/` routes;
- valid edits launch with the selected mount root as `command.Dir`; forged,
  cross-mount, missing-mount, hidden, and symlink-escaping edit paths are
  rejected; loopback host, origin, token, method, and content-type
  protections are unchanged;
- the registry gains one entry per mount with the `mount` field, the
  single-root form writes none, legacy entries still load, and `open`
  produces mounted URLs;
- concurrent requests to different mounts share no mutable route state;
- title caching remains correct for equal filenames in different mounts;
- single-root HTML, registry, and editor tests continue passing without
  changed URLs;
- `build` tests continue passing after the validation extraction.

## Expected scope

No new dependencies. Expect a moderate refactor of `serve` flag parsing,
route resolution, URL construction, navigation, editor validation, and the
registry, plus focused integration tests. The `build` reuse removes the
naming, hub-page, and validation work from the earlier estimate; a
reasonable implementation is roughly 300 to 500 lines of production and test
changes.

## Non-goals

- automatic mount names derived from directory basenames;
- serving the common parent of configured mounts;
- allowing symlinks to cross between mounts;
- global search or a merged directory tree;
- cross-mount Markdown link rewriting;
- per-mount editor commands;
- multiple listeners or one port per mount;
- changes to `save` or to `build` beyond sharing the validation code.
