# Feature: remote file access

## Goal

Allow `mdfmt serve` to browse and render a directory tree whose files are not
on the machine running `mdfmt`:

```text
browser -> local mdfmt server -> remote source -> files
```

The server should use the same directory, Markdown, source-code, and plain-text
views for local and remote content. Pages must make the source identity and
remote status obvious without exposing credentials or sensitive connection
details.

The initial architecture should separate filesystem presentation from remote
connectivity. A local source implementation must preserve today's behavior. A
fake remote implementation should make latency, disconnections, permissions,
and consistency behavior testable before choosing or implementing an actual
transport.

## Scope and non-goals

This plan covers:

- a source abstraction shared by local and future remote implementations;
- logical path and metadata types;
- remote-aware routing and rendering;
- source identity in titles, breadcrumbs, and navigation;
- error classification, cancellation, and refresh-based reconnection;
- directory-listing performance over a high-latency source;
- cache identity and consistency;
- capabilities, including an unresolved design for remote editing;
- a conformance test suite and a failure-injecting fake source.

The first implementation does not need to choose SSH, SFTP, an agent protocol,
an object store, a network filesystem, or another connectivity mechanism. It
also does not need to implement authentication, credential storage, host-key
verification, or tunneling. Those choices must eventually satisfy the source
contract and security rules described here.

Static `save` output remains local and Markdown-only. The proposed `build`
command remains local unless remote build inputs are designed separately.

## Source abstraction

Do not mirror every `os` call mechanically. The current local implementation
can afford an operation sequence such as:

1. `ReadDir`;
2. resolve every child symlink;
3. `Stat` every child;
4. read every Markdown file whose title is shown.

That becomes an N+1 or N+M remote request pattern. The abstraction should make
the operations used by the UI efficient and should return directory-entry
metadata in bulk.

A starting point is:

```go
type Source interface {
    Descriptor() SourceDescriptor
    Capabilities() SourceCapabilities

    Stat(ctx context.Context, path SourcePath) (Entry, error)
    ReadDir(ctx context.Context, path SourcePath) (DirectorySnapshot, error)
    ReadFile(ctx context.Context, path SourcePath, limit int64) (FileSnapshot, error)
    OpenRaw(ctx context.Context, path SourcePath) (RawFile, error)
}

type SourceDescriptor struct {
    ID          string
    DisplayName string
    Host        string
    Remote      bool
}

type SourceCapabilities struct {
    RawStreaming bool
    Editing      bool
    Symlinks     bool
    Versions     bool
}
```

`DirectorySnapshot` should contain the directory's metadata and metadata for
all children. It must not require a follow-up `Stat` per child. `FileSnapshot`
should contain content and the metadata/version of the file that was actually
opened, avoiding a racy pre-read `Stat` where the backend can do so.

`RawFile` may contain an `io.ReadCloser`, metadata, and any supported range or
seek capability. The interface should not require every remote backend to
implement `io.Seeker`.

Go's `io/fs` interfaces are useful inspiration for names and error semantics,
and a local implementation can use them internally. Plain `fs.FS` is not a
sufficient service boundary because it lacks:

- `context.Context` cancellation and deadlines;
- bulk metadata requirements;
- backend identity and UI metadata;
- remote availability and consistency errors;
- capability negotiation;
- explicit preview limits;
- a portable symlink/security-boundary contract.

## Logical paths

Represent source paths independently of host filesystem paths. A `SourcePath`
is rooted, slash-separated, and made only from validated logical components.
It is not an absolute Unix path, Windows path, URL, or transport URI.

The HTTP layer remains responsible for:

- decoding and validating URL components;
- rejecting `.`, `..`, empty, slash-containing, backslash-containing, NUL, and
  hidden components according to the current policy;
- keeping URL path tokens separate from source paths;
- generating escaped relative links without source credentials.

The source implementation is responsible for:

- resolving a logical path only beneath its configured root;
- applying its own case-sensitivity and separator rules;
- preventing symlink or alias escapes atomically;
- never accepting an unchecked native path from the HTTP handler;
- returning only metadata and content belonging to that source.

The local source adapts the existing `filepath.Abs`, `EvalSymlinks`,
`withinRoot`, and hidden-component checks. A remote source must enforce an
equivalent jail using remote semantics rather than asking the server to reason
about a remote native path.

## Source identity and capabilities

Every configured source needs a stable, non-secret ID. The ID is used for
cache keys and registry references; it must not embed a password, bearer token,
private key path, or complete connection URI.

Keep machine-readable identity separate from presentation:

```text
ID             stable opaque identity
DisplayName    human-friendly source/root name
Host           escaped host or location label
Remote         visual-locality hint
Capabilities   behavior the source actually supports
```

Prefer capability checks to assumptions based only on `Remote`. A local source
might be read-only, while a remote implementation might support safe editing.
Rendering should depend on `Capabilities().Editing`, for example, rather than
on `!Descriptor().Remote`.

Descriptor values are untrusted text. Escape them through `html/template` and
never permit a backend to supply HTML fragments, CSS class names, or arbitrary
URLs for the page.

## CLI addressing and registration

Local CLI paths currently serve several roles simultaneously:

- selecting the root;
- establishing filesystem identity;
- providing the display basename;
- allowing `mdfmt open` to find the longest matching registered root.

Those roles need to be separated for remote sources. A future transport may
use a URI-like source specification, but parsed credentials must be removed
before creating display strings, logs, cache keys, or registry data.

The eventual CLI needs concepts equivalent to:

```text
source specification     how the backend connects
source ID                stable registry/cache identity
display name             UI and route label
logical root             backend-relative security boundary
```

Do not finalize syntax such as `ssh://host/path` in the abstraction phase. The
source factory can initially accept local paths and test-only fake source
descriptors.

`~/.mdfmt/ports.json` currently keys entries by resolved absolute local root.
It will eventually need a source-aware key or a versioned source registry. It
must store only the minimum information required by `open`; credentials and
private transport configuration belong in a separate protected mechanism.

`mdfmt open` cannot resolve a remote logical path with `filepath.EvalSymlinks`
or `filepath.Rel`. It will need either:

- an explicit source ID plus logical path;
- a source-specific URI handled by the configured backend;
- or another unambiguous remote path syntax.

Local `open` behavior should remain backward compatible.

## Routing and page generation

Refactor `markdownServer` so request handlers operate on a selected `Source`
and `SourcePath`, not directly on `server.root` and absolute filenames.

For one source, preserve the current URL layout. When the multiple-roots plan
is implemented, each mounted root should own a source and logical root. Route
selection should return at least:

```text
selected source
logical source path
URL components or mount prefix
```

The following operations must become source-aware:

- resolving a requested path;
- listing a directory;
- reading and rendering a document;
- serving Raw content;
- extracting and caching Markdown titles;
- producing modification and size metadata;
- creating breadcrumbs and parent navigation;
- determining whether Edit and other actions are available.

Generated HTML must continue to omit the random URL path token. Source IDs,
hostnames, transport addresses, and credentials must not be inserted into
document-authored links. Relative navigation should continue inheriting the
token from the browser's current URL.

## Remote-aware UI

Remote content must be visually distinguishable from local content without
making every page noisy. Add source presentation data to the common page model
so normal pages and error pages use it consistently.

Suggested presentation:

- window title: `filename · host · mdfmt`;
- breadcrumb root: `display-name @ host` or an equally compact form;
- a persistent `Remote` badge or connection indicator near the breadcrumbs;
- an accessible text label, not color alone;
- a tooltip or details line that identifies the logical source without showing
  credentials or an absolute private path.

Local pages may omit the badge and retain today's compact title. If the local
hostname is displayed, it should use the same descriptor field rather than a
special template branch.

Connection state shown in a page is necessarily a snapshot. Avoid presenting
a stale page badge as a live guarantee. A live status indicator or polling
endpoint is optional and outside the first version.

## Errors, cancellation, and reconnection

Define source errors that handlers can classify without parsing strings:

```text
not found
permission denied
source unavailable
deadline exceeded
operation cancelled
file changed during operation
preview too large
unsupported capability
internal/backend failure
```

Wrap underlying errors while preserving `errors.Is` or typed classification.
Map them consistently to HTTP responses, for example:

- not found -> `404`;
- permission denied -> `403`;
- unavailable -> `503`;
- backend deadline -> `504`;
- preview too large -> a normal unavailable-preview page or `413`, according
  to the generic viewer policy;
- unexpected backend failure -> `500` with a sanitized message.

Every source operation receives the HTTP request context and a bounded server
deadline. Browser cancellation must stop remote work. The backend may make one
short, bounded reconnect attempt where safe, but handlers must not retry
indefinitely.

The expected recovery model is deliberately simple:

1. a disconnected request fails loudly;
2. the server remains healthy and does not permanently poison the source;
3. each new request attempts the source again;
4. after connectivity returns, refreshing the page succeeds without restarting
   `mdfmt`.

Return a clear error page containing source display identity, operation type,
and a refresh suggestion. Do not show credentials, raw protocol errors,
private key locations, or full connection strings in browser responses.

## Partial responses and raw streaming

Rendered directory and document pages should gather all required remote data
before sending response headers. This permits a disconnect to become a clean
error page rather than half of a valid document.

Raw streaming is different. Once `200 OK` and some bytes have been sent, a
mid-stream disconnect can only produce a truncated response. The handler
cannot replace it with a `503` page.

Therefore:

- bounded Markdown/source/text previews should be fully read before rendering;
- a backend without reliable streaming may report `RawStreaming: false`;
- the UI should omit Raw/Download actions when unsupported;
- streaming errors should be logged with source ID and logical path;
- responses should include version/length metadata when available so clients
  can detect truncation;
- automatic resume/range support is a later transport-specific capability.

## Consistency and versions

Remote files can change between directory listing, metadata lookup, and read.
Where available, expose an opaque backend version such as an ETag, generation,
revision, or inode/version pair. Do not assume modification time and size alone
identify content reliably.

Use versions for:

- cache keys and invalidation;
- conditional reads where the backend supports them;
- detecting a file that changed during rendering;
- any future remote edit conflict check.

A backend without stable versions may leave `Entry.Version` empty and accept
weaker freshness guarantees. The UI must not invent precision that the backend
cannot supply.

Directory pages are not transactional snapshots unless the transport offers
that feature. It is acceptable for a file to disappear after being listed; the
subsequent request should return a normal `404` or changed-file response.

## Caching

Cache keys must include:

```text
source ID
logical path
opaque source version, or fallback modification time and size
```

Never key remote content by an absolute local cache filename alone. Two hosts
may expose identical logical paths.

The initial implementation should cache only derived data already cached
locally, such as Markdown titles. Avoid a general persistent content cache
until its security, expiration, storage ownership, and credential boundaries
are specified.

If content is cached later:

- use a private directory with restrictive permissions;
- partition it by opaque source ID;
- bound disk usage and retention;
- use atomic writes;
- prevent symlink substitution;
- never put credentials in filenames or metadata;
- make offline/stale rendering explicit in the UI;
- provide a safe cleanup policy.

Remote errors must not be cached as permanent results. A fresh browser request
after reconnection should try the source again.

## Editing remote files: design still required

Remote editing is intentionally unresolved. The current Edit action launches
a local editor with an absolute local filename, which has no direct equivalent
for a remote source.

Model editing as an optional source capability now, but keep remote sources
read-only until one of the following workflows is selected and specified.

### Option A: local cache plus watched write-back

One possible workflow is:

1. fetch the remote file into a private local cache;
2. remember the remote opaque version used for the fetch;
3. launch the configured editor on the cached file;
4. watch for editor saves, including atomic rename-and-replace behavior;
5. debounce changes and upload a complete new version;
6. use an expected-version precondition so a concurrent remote change cannot
   be overwritten silently;
7. report successful synchronization or a visible conflict;
8. stop watching and clean up according to an explicit session lifecycle.

This is feasible, but it introduces important questions:

- Many editor commands return immediately unless a transport-specific `--wait`
  option is used. How does `mdfmt` know when the edit session is finished?
- Editors may save by truncating, writing a temporary file, renaming, changing
  permissions, or producing several rapid events. Which state is uploaded?
- What happens when the connection drops after a local save?
- Are pending changes queued across server restarts, and where is that durable
  state stored?
- How are upload failures shown to the user if the browser page is no longer
  open?
- How does the user resolve a conflict between the cached base version, local
  edit, and newer remote version?
- When is sensitive cached content deleted, and can it be recovered after a
  crash?
- Should one cached file be shared by two edit sessions?

No implementation may silently use last-writer-wins. A failed version
precondition must preserve the local edit, preserve the newer remote content,
and require an explicit resolution.

### Option B: explicit fetch/edit/publish session

Instead of uploading on filesystem watcher events, `mdfmt` could create an edit
session and require an explicit publish action from the browser or CLI. This
makes synchronization and error reporting clearer but is less seamless than
ordinary editor Save behavior.

The session would need status, base version, local cached path, dirty state,
publish result, conflict handling, cancellation, and cleanup. Browser CSRF and
editor-launch protections still apply.

### Option C: backend-native editor integration

A source may provide a native editing action, such as launching an editor's
remote workspace support or running an editor on the remote host. This avoids
generic cache synchronization but is transport- and editor-specific. It should
be exposed through a capability rather than embedded into the general read
interface.

### Initial decision

For the first remote browsing release:

- remote sources advertise `Editing: false`;
- no Edit controls are rendered for them;
- the existing local editor behavior remains unchanged;
- the source and page models are capability-aware so editing can be added
  without another routing redesign;
- a fake editable source may be used later to test the chosen protocol before
  integrating a real transport.

Any later remote editing plan must specify cache security, session lifecycle,
version preconditions, offline behavior, conflict resolution, failure
visibility, and recovery after process termination.

## Security boundaries

The random URL path token remains required by default for remote sources. A
remote source may contain substantially more sensitive content than a small
local documentation root, so broadening access increases the importance of:

- loopback binding by default;
- path-token validation on every endpoint;
- token-free generated HTML;
- restrictive CSP and `nosniff` headers;
- hidden-component exclusion;
- source-root confinement;
- output escaping for filenames, hostnames, errors, and file content;
- no credentials in URLs, HTML, logs, registry entries, or cache paths;
- request deadlines and preview-size limits;
- bounded concurrency to prevent one browser page from exhausting a remote
  connection pool.

If non-loopback binding is supported with remote sources, path tokens alone
are not user authentication. Network authentication and authorization would
need a separate design.

The source implementation should expose read-only operations by default. A
transport having write credentials does not imply that the UI may edit files.

## Performance and concurrency

The HTTP server handles requests concurrently, so a remote source must define
whether its client/session is concurrent-safe. The adapter may use a bounded
connection pool or serialize operations, but it must honor request contexts.

Avoid remote work proportional to the number of directory children beyond the
single bulk listing response. In particular:

- `ReadDir` returns child type, size, time, and version together;
- directory rendering does not read every Markdown file merely to find titles
  unless the backend provides titles cheaply or an asynchronous/cache policy
  is added;
- Chroma matching remains local and occurs only for the opened file;
- sidebar generation reuses the same directory snapshot as the main view;
- duplicate `Stat` calls within one request should be eliminated.

For remote directories, the initial UI may show filenames instead of uncached
Markdown H1 titles. A later lazy title cache can populate titles after files
have been opened, or a transport may provide an efficient batch-title feature.
Do not hide network round trips behind an apparently local helper.

## Testing

Create a backend conformance suite that runs against the local source and a
deterministic in-memory/fake remote source. Cover:

- root, directory, file, and missing-path lookup;
- path traversal and hidden-component rejection;
- symlink or alias escape behavior;
- bulk directory metadata without per-child source calls;
- Markdown and generic text reads with size limits;
- binary and invalid-UTF-8 handling from the generic viewer plan;
- opaque versions and changed-during-read errors;
- context cancellation and deadlines;
- permission-denied, unavailable, timeout, and internal errors;
- reconnect on a later request after a simulated disconnection;
- concurrent requests and backend concurrency limits;
- HTML escaping of display names, hosts, paths, and backend errors;
- path-token enforcement and absence from generated HTML;
- Raw action omission when streaming is unsupported;
- local Edit controls preserved and remote Edit controls omitted;
- caches separated for sources with identical logical paths;
- no credentials or native backend paths in responses, logs, or registry data.

The fake source should support scripted failures at each operation and a stream
that fails after several bytes. This makes error behavior testable without a
real network dependency.

Keep the existing server tests running through the local source adapter. They
are the compatibility contract for URL layout, Markdown rendering, directory
sorting, security headers, path tokens, editor controls, and filesystem
confinement.

## Documentation

Once a real transport exists, document:

- source syntax and display identity;
- where credentials are configured and how they are protected;
- host verification or equivalent trust behavior;
- read-only versus editable source capabilities;
- timeout, disconnection, refresh, and raw-streaming behavior;
- cache location, permissions, retention, and cleanup if caching is enabled;
- the fact that a local browser can view data retrieved from another machine;
- the security implications of non-loopback binding;
- remote editing conflict and recovery behavior, if editing is implemented.

UI examples should visibly distinguish a remote source from a local one.

## Implementation outline

1. Define logical paths, descriptors, capabilities, snapshots, and typed source
   errors.
2. Implement a local source adapter and move filesystem resolution, listing,
   reads, and root enforcement behind it without changing behavior.
3. Refactor server handlers, page data, title caching, and Raw responses to use
   a selected source and logical path.
4. Add source identity to titles, breadcrumbs, common page UI, and error pages.
5. Add a deterministic fake remote source with latency, cancellation,
   disconnection, permissions, versions, and mid-stream failure injection.
6. Remove N+1 metadata operations and define the remote Markdown-title policy.
7. Version the port/source registry and design unambiguous remote addressing
   for `mdfmt open` without storing secrets.
8. Keep remote editing disabled while exposing capability-aware actions.
9. Run the full race-enabled CI suite and security regression tests.
10. Choose and implement a real remote transport as a separate phase.

Ignoring the transport, this is a moderate cross-cutting refactor rather than
a difficult rendering feature. The main risks are inefficient interface
granularity, source-root enforcement, partial responses, cache identity, and
accidentally treating remote editing as ordinary local file editing. A robust
source abstraction, local migration, fake backend, UI identity, and tests are
likely three to five focused days before actual connectivity work begins.
