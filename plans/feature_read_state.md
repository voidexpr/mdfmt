# Feature: read state for documents

## Goal

Make it easy to see which documents have been read, which have not, and
which changed after they were read:

- a document can be marked as read or unread explicitly;
- a mark is pinned to the version of the document it was made on, so a
  document edited afterwards shows as updated rather than read;
- every document keeps a short history of its last state changes, so a mark
  made by accident can be inspected and reversed;
- every document keeps a short trail of reading positions, so reading can
  resume on another device and a quick skim through the document does not
  lose the place where it was actually being read;
- every document being read has a progress, the percentage reached, shown
  in the listings so a half-read document is visible as such;
- a document modified after it was read, or while it was being read, is
  shown as exactly that: `read/modified`, `32%/modified`;
- the sidebar, the directory listing and the toolbar show the state;
- the state works offline, in the home-screen app included;
- with `serve`, the state can be shared between devices, typically a desktop
  browser and the phone's home-screen app.

## Design summary

The browser's own storage is the primary store in every mode. `build`
output gets exactly that and nothing more. `serve` optionally adds a sync
endpoint that the same client code treats as a peer: the client merges its
records with the server's and both end up with the union.

```text
build                   local store only
serve                   local store only (default)
serve --read-sync       local store, merged with a SQLite file at the root
serve --read-db PATH    the same, with the SQLite file at PATH
```

This is one client code path. It follows from the offline requirement: the
phone marks documents with no network, so marks must land locally first and
reach the server later. A server-only store could not do that.

The short history asked for as an undo aid is also what makes the merge
possible, so the record format below is designed for both. Marks and
reading positions travel in the same record and through the same sync.

## Document version

Read state must notice that a document changed. The version of a document
is a short prefix (12 hex characters) of the SHA-256 of its Markdown source
bytes, in both `serve` and `build`.

- Hashing the source rather than the rendered page means a new binary, a
  theme change or a rebuild does not turn every read document into an
  updated one, and `serve` and `build` agree on the value.
- Hashing content rather than using the modification time means a `touch`,
  a checkout or a sync client rewriting the file does not flip the state.
- `serve` already reads the whole file to render a document page, so the
  page's own version is free. For listings, the title cache (`titleCache`
  in `server.go`) already opens each file and is invalidated by
  modification time and size; it stores the version next to the title.
- `build` hashes each source file in the walk that renders it.

The version is rendered where the script needs it:

- `<html data-version="...">` on a document page;
- `data-version` on each file entry of the sidebar (`a[data-name]`) and of
  the directory table (`tr[data-kind="file"]`);
- a `version` field on document entries of the site index (`site.json`),
  used for folder rollups (see UI). This is distinct from the `stamp`
  field of the offline sync plan, which describes the cached response and
  in `build` hashes the rendered output.

`save` standalone files render none of this and do not get the feature,
like the recent list.

## Records and the local store

One `localStorage` entry, key `mdfmt.read`, holds a JSON object mapping a
document route to its record. A record has two lists, both newest first,
and one value: `marks`, the log of state changes, at most 5; `trail`, the
reading positions; and `progress`, how far the document has been read.
The last two are described in the next section.

```json
{
  "guide/setup.md": {
    "marks": [
      { "at": 1789000300000, "state": "read", "version": "3fa9c2d1e07b" },
      { "at": 1789000100000, "state": "unread", "version": "3fa9c2d1e07b" },
      { "at": 1788000000000, "state": "read", "version": "a41b09c77e02" }
    ],
    "trail": [],
    "progress": null
  }
}
```

- The route is the document's path relative to the site root, the same
  identity the recent list uses (`identity` in `app.js`).
- `at` is a millisecond timestamp from the device that made the change.
- `state` is `read` or `unread`.
- `version` is the document version the change was made on.

There is no separate "current state" field. The displayed state, and the
badge the listings show for it, derive from the newest mark, the progress
and the document's current version:

| Newest mark             | Progress                | Badge           |
|-------------------------|-------------------------|-----------------|
| `read`, same version    | any                     | `read`          |
| `read`, other version   | any                     | `read/modified` |
| none or `unread`        | none                    | none (unread)   |
| none or `unread`        | 32%, same base version  | `32%`           |
| none or `unread`        | 32%, other base version | `32%/modified`  |

A `read` mark takes precedence over progress: a document that was read,
then changed, then partly reread stays `read/modified` until it is marked
again, which is the one action that settles it.

An explicit `unread` change is kept rather than deleting the record: it is
what lets an "unread" made on one device override an older "read" on the
other during a merge.

The store is separate from `mdfmt.recent`, which is capped at 20 documents
and ordered by recency. Read records are not capped by count. A record
with a full log and a full trail is under 1 KiB, so a few thousand
documents stay inside the `localStorage` quota. All access is wrapped in
`try`/`catch`; without storage the controls are hidden.

Known limitation, shared with the recent list: `localStorage` is per
origin, so two sites on one origin under different path tokens share the
store and colliding routes would share a record.

## Position trail

The recent list already saves one position per document, as a heading ID
and a pixel offset, on this device only. That is enough to reopen a
document where it was left on the same device. It cannot be shared, since
pixels differ between a desktop and a phone, and it remembers only the
last place the page was scrolled to, which after a quick skim is not the
place where reading stopped.

The trail records runs. A run is a stretch of continuous scrolling at
reading pace; a jump ends it and starts the next one.

```json
{
  "start": 1789000000000,
  "device": "a1b2c3",
  "updated": 1789000540000,
  "version": "3fa9c2d1e07b",
  "heading": "installing-the-binary",
  "part": 0.4,
  "whole": 0.31,
  "dwell": 512
}
```

- `start` and `device` identify the run. `device` is a random 6-character
  ID generated once per browser store (`mdfmt.device`). It carries no
  meaning beyond telling this device's runs from another's.
- `heading` and `part` are the position in a form that survives a
  different screen width: the current heading, by the rule the recent list
  uses, and how far the scroll line is through that heading's section,
  from 0 to 1. `heading` is empty in a document without headings.
- `whole` is the same position as a fraction of the whole document. It is
  what the UI shows as a percentage, and the fallback when the heading no
  longer exists.
- `dwell` is the reading time of the run in seconds: the sum of the gaps
  between its successive samples, each gap capped at 2 minutes. A run made
  of a single sample has a `dwell` of 0, however long the page then stays
  open at that spot.
- `updated` and `version` are the time and the document version of the
  run's last sample.

### Collapsing

Samples come from the existing debounced save (`savePosition`), which
fires 250 ms after scrolling stops and when the page is hidden. Each sample
is compared with the newest run of this device for the document:

- within about one and a half screens forward, or one screen backward, of
  that run's position, and less than 30 minutes after its `updated`: the
  sample continues the run. Its position, `updated` and `dwell` are
  overwritten in place. Ordinary reading, including scrolling back a
  paragraph, therefore stays one entry however long it lasts.
- otherwise the sample starts a new run. The previous run is left as it
  was, which is the point: it still holds the place reading had reached.

A fast flick through the document produces a single sample where it comes
to rest, far from the run, so it opens a new run rather than dragging the
reading run along. A table-of-contents jump, a followed fragment and the
"top" link do the same.

The trail keeps at most 6 runs per document. When a new run would exceed
that, the run with the smallest `dwell` among those older than the newest
two is dropped, so skims are forgotten before reading runs.

### Reading runs and resuming

A run counts as a reading run when its `dwell` is at least 20 seconds. A
skim to the bottom and back to the top leaves runs of one or two samples,
with a `dwell` near 0 even when the page is then left open; the stretch
that was actually being read has many samples and minutes.

This is a heuristic and is treated as one. It decides what is offered, not
what is forced:

- opening a document restores, as today, this device's last position from
  the recent list, under the existing conditions (no fragment, a fresh
  navigation);
- when this device has no position for the document, the newest reading
  run from any device is restored instead. This is the case of starting on
  the desktop and continuing on the phone;
- when the restored position is not the newest reading run, a small chip
  offers it: "Continue at Installing the binary · 31% · 2h ago", with
  "other device" appended when the run is not this device's. A tap jumps
  there. The chip disappears on the first scroll or after a few seconds;
- the whole trail is listed in the read popover (see UI), each run a link
  to its position, so any of the big movements can be retraced by hand.

No chip is shown for a document that is read at its current version.

A position resolves to the heading's top plus `part` of its section's
current height. When the heading no longer exists in the document,
`whole` of the current document height is used.

The recent list keeps its own heading and pixel offset and stays the
source of same-device restoration, which is exact where the trail is
approximate. Both are written from the same `savePosition` call.

### Progress

The percentage read is tracked as its own value rather than derived from
the trail, because the trail forgets runs and the percentage must not.

```json
{ "start": 1789000000000, "base": "3fa9c2d1e07b",
  "at": 1789000540000, "whole": 0.32 }
```

- `whole` is the furthest position a reading run has reached, as a
  fraction of the document. It only grows: scrolling back to reread does
  not lower it, and a skim does not raise it, since only runs that qualify
  as reading runs advance it.
- `whole` is the scroll offset divided by the maximum scroll offset, so
  the end of the document is 1 and shows as 100%. The same definition is
  used for the `whole` of a run.
- `start` is when this progress began and `base` the document version at
  that moment. `at` is the time of the last advance.
- A document is "modified while being read" when its current version
  differs from `base`. Continuing to read does not clear this; `base` is
  never rewritten. The `/modified` part of the badge stays until the
  document is marked, as with `read/modified`.

A mark starts a new cycle. Progress whose `start` is older than the newest
mark is ignored and replaced by the next advance, so marking a document as
unread also resets its percentage without anything having to be deleted.

Reaching 100% does not mark the document as read. It shows `100%`, and the
end-of-article button is right there.

## Merge rule

Merging two `marks` logs for the same route:

1. take the union of their changes;
2. drop duplicates (same `at`, `state` and `version`);
3. sort by `at`, newest first;
4. keep the first 5.

Merging two `trail` lists:

1. take the union of their runs;
2. where both sides hold the same run (same `start` and `device`), keep
   the copy with the larger `updated`;
3. sort by `updated`, newest first;
4. apply the 6-run limit with the dropping rule above.

Only the owning device ever extends a run, so step 2 never has to combine
two diverging copies.

Merging two `progress` values, after the marks are merged:

1. discard a value whose `start` is older than the newest mark;
2. with one value left, keep it;
3. with two, the result takes `start` and `base` from the one with the
   earlier `start`, the larger `whole` and the later `at`.

Step 3 means two devices that each read part of a document end up with
the furthest point either reached, and if one of them began before a
modification the result still says so.

Merging two stores applies these per route, keeping routes present on either
side. The rule is commutative and idempotent, so the client and the server
can each apply it in any order and converge. The newest change wins, by
device clocks. Clock skew between devices can misorder changes made within
the skew; for a single reader moving between devices this is accepted.

The rule exists twice, in `app.js` and in Go. It is small enough that a
shared test table (same inputs, same expected output) is the guard against
drift.

## `serve` sync

Off by default. `serve` is described as a read-only website; this is its
first write that is not a loopback editor launch, so it is opt-in:

- `--read-sync` enables it with the database at its default location, the
  root of the served tree;
- `--read-db PATH` enables it with the database at `PATH`. It needs no
  `--read-sync` beside it, and giving both is accepted.

When enabled and the request is allowed to write read state (see Access),
pages carry `data-read-sync` on `<html>` with the endpoint's relative URL.
The script syncs only when the attribute is present, so `build` output and
a default `serve` never issue these requests.

Endpoint, beneath the asset prefix and therefore beneath the path token:

- `GET .mdfmt/read.json` returns the user's store, in the same JSON
  shape as the local store, with an `ETag`.
  `If-None-Match` yields `304` when nothing changed.
- `POST .mdfmt/read.json` takes a JSON object of records, merges it into
  the stored state with the merge rule, persists, and returns the merged
  store with its `ETag`.

Client behaviour:

- marking a document writes the local store first, adds the route to a
  dirty set (`mdfmt.read.dirty`), then attempts a `POST` of the dirty
  records. On success the response replaces the local store, merged with
  anything marked in the meantime, and the dirty set is cleared;
- on each page load, a conditional `GET` when the dirty set is empty, or
  the `POST` when it is not. The `online` event triggers the same;
- position samples update the local store and the dirty set only. They are
  not sent as they happen. The dirty records go out when the page is
  hidden (`visibilitychange`, `pagehide`), with `fetch` and `keepalive` so
  the request survives the page, and otherwise with the next page load;
- a failed request changes nothing visible. The local state is already
  correct and the dirty set retries on the next load.

The service worker does not store `read.json` and does not intercept the
`POST`, like the editor requests. The local store is the offline copy.

### Server storage

The state lives in one SQLite database. By default it sits at the root of
the served tree:

```text
ROOT/.mdfmt.sqlite
```

- The state stays with the documents. Whichever machine or process serves
  the root finds it, and nothing depends on a path under `$HOME`.
- The name starts with a dot, so the existing rules already keep it out of
  listings, out of the site index and unreachable by URL in `serve`, and
  out of the walk in `build`.
- With several mounts (the multiple-roots plan), each mount root has its
  own database.

`--read-db PATH` puts the database anywhere else, for a read-only root, for
a root inside a folder that a sync client mirrors, or simply to keep the
tree clean:

- `PATH` is used as given, relative to the working directory when it is
  not absolute. Its directory must exist; the file is created when
  missing.
- A custom database belongs to one served root. Routes are relative to the
  root, so pointing two different roots at one file would mix their
  records. With mounts, one custom database serves all mounts of the
  process, and routes carry the mount prefix.
- A path inside the served tree is allowed. The file is not Markdown, so
  it is not listed or served whatever its name.

In both cases startup fails with a clear error when the database cannot be
created or opened for writing.

#### Schema

Every table hangs off `users`. There is one user today, created with the
database, and nothing in the feature refers to accounts. The point is that
adding them later is a matter of policy, not of schema: a second user is a
second row, and every table already says whose data a row is.

```sql
CREATE TABLE users (
  id       INTEGER PRIMARY KEY,
  name     TEXT NOT NULL UNIQUE,
  created  INTEGER NOT NULL,
  revision INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE marks (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  route   TEXT NOT NULL,
  at      INTEGER NOT NULL,
  state   TEXT NOT NULL,
  version TEXT NOT NULL,
  PRIMARY KEY (user_id, route, at, state, version)
) WITHOUT ROWID;

CREATE TABLE runs (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  route   TEXT NOT NULL,
  device  TEXT NOT NULL,
  start   INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  version TEXT NOT NULL,
  heading TEXT NOT NULL,
  part    REAL NOT NULL,
  whole   REAL NOT NULL,
  dwell   INTEGER NOT NULL,
  PRIMARY KEY (user_id, route, device, start)
) WITHOUT ROWID;

CREATE TABLE progress (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  route   TEXT NOT NULL,
  start   INTEGER NOT NULL,
  base    TEXT NOT NULL,
  at      INTEGER NOT NULL,
  whole   REAL NOT NULL,
  PRIMARY KEY (user_id, route)
) WITHOUT ROWID;
```

- The single user is the row named `default`. `readAccess` (see Access)
  returns a user name per request; today it always returns that one.
- `users.revision` is a counter incremented by every `POST` that changed
  something for that user. It is the `ETag`.
- `PRAGMA foreign_keys = ON` is set on every connection, so deleting a
  user removes all of their state and no row can exist without an owner.
- The rest of each primary key is the identity the merge rules use, so a
  merge is an `INSERT OR IGNORE` for marks and an upsert guarded by
  `updated` for runs, followed by the trims to 5 marks and 6 runs. A
  `POST` is one transaction.
- `PRAGMA user_version` carries the schema version. Opening a database
  with an older version runs the migrations in order; a newer version than
  the binary knows is an error, not a downgrade.
- Extending the feature means adding tables or columns behind a version
  bump, each new table keyed by `user_id` in the same way. The share-link
  plan would add its links here, and the offline sync plan's
  subscriptions could follow if they ever need to be shared.

Driver: `modernc.org/sqlite`, the pure Go port. The project builds without
cgo today and keeps doing so; the cost is a larger binary.

Journal mode is the default rollback journal, not WAL. WAL keeps two extra
files beside the database for as long as it is open, which is what breaks
when a folder is copied or synced while the server runs. A `busy_timeout`
covers a second process serving the same root.

### Request validation

- `POST` requires `Content-Type: application/json` and an `Origin` header
  whose host equals the request's `Host`. `Sec-Fetch-Site`, when present,
  must be `same-origin`. No CORS headers are ever sent.
- The body is capped at 1 MiB.
- Each route must resolve, through the same path resolution the pages use,
  to an existing listed document under the root. Other routes are dropped,
  which also prunes records of deleted documents over time.
- `state` must be `read` or `unread`, `version` and `base` must be 12 hex
  characters, and timestamps must be positive integers no more than a day
  in the future. Logs are truncated to 5 changes.
- In a run, `device` must be 6 hex characters, `heading` a string of at
  most 200 characters, `part` and `whole` numbers from 0 to 1, and `dwell`
  a non-negative integer. Trails are truncated to 6 runs.
- The endpoint never reads or writes anything but the database, and only
  through prepared statements.

## Access

Read-state sync needs an answer to two questions per request: may it write,
and as which user. The plan isolates that in one function so the policy
can grow without touching the sync code or the schema:

```go
// readAccess reports the read-state user of a request and whether the
// request may write that user's state.
func (s *markdownServer) readAccess(r *http.Request) (user string, writable bool)
```

Initial policy: with sync enabled, every request that reaches the server
through the path token reads and writes as the user `default`.
Authentication stays in the reverse proxy. mdfmt gains no account or
cookie handling.

Accounts, if they come, are a different policy behind the same function:
for example the user name taken from a header the proxy sets after
authenticating, with the `users` row created on first use. The storage and
the sync code would not change.

The Edit button is a separate capability and stays as it is. It launches
an editor on the server's machine and is restricted to loopback requests
with its own token. It should not be tied to read-state writes, which must
work from the phone.

### Relation to share links

Sharing one folder or one file with someone else, read-only, is a separate
feature with its own plan. It fits the same seam. A link would be a token
mapped to a scope (a subtree or a single document), a mode, and a user:

- the existing path token is the owner link: whole root, writable, user
  `default`;
- a share link has its own token and is rooted at its scope, so nothing
  above the scope is addressable, as the pages already compute everything
  relative to `data-root`;
- a read link renders no `data-read-sync`; its reader still gets local
  read state in their own browser, exactly like a `build` site;
- a write link for another person would carry its own user, so their
  marks never touch the owner's.

`readAccess` is where a link's mode and user would be returned. The
server would translate a scoped link's routes to root-relative routes
before storing them. None of this is built here; the plan only keeps the
seam.

## UI

### Document page

- A toolbar button `data-read-toggle` in `.page-actions` shows the state
  with a check icon: outlined when unread, filled when read, with a dot
  when modified since. While the document is being read the button shows
  the percentage beside the icon. It opens a popover (the existing
  `registerPopover` mechanism) with the primary action, "Mark as read" or
  "Mark as unread", and the change log as plain lines, for example
  "Read · 3d ago · Sep 27, 2026 14:05".
- Below the log, the popover lists the position trail, newest first, each
  run a link that scrolls to its position: the heading title, the
  percentage, the age, the time spent when it is a reading run, and
  "other device" where it applies. Skims are shown dimmed.
- The "Continue at" chip described under Position trail appears above the
  toolbar on narrow screens and below it on wide ones.
- The popover offers "Undo last change" whenever the log has a previous
  entry. Undo appends a new change that repeats the previous entry's state
  and version, so it merges like any other change. The original times
  remain visible in the log.
- A "Mark as read" button at the end of the article gives a one-tap action
  at the point where reading finishes. It is hidden when the document is
  already read at its current version.

### Listings

The script gives every file entry a badge from the table under Records,
using the entry's `data-version` and the local store:

| Badge           | Meaning                                              |
|-----------------|------------------------------------------------------|
| none            | unread, not started; an accent dot precedes the name |
| `32%`           | being read, furthest point reached                   |
| `32%/modified`  | being read, and the document changed since it began  |
| `read`          | read at its current version; name in secondary text  |
| `read/modified` | read, and the document changed since                 |

- The directory table gains a "Read" column on wide screens, between
  "Ago" and "Size", sortable like the others (unread, in progress by
  percentage, read).
- In the narrow two-line list the badge sits on the second line, after
  the date.
- Sidebar entries show the badge in the detail slot before the age, where
  space is short, with `modified` abbreviated: `read/mod`, `32%/mod`.
- The `/modified` part uses a distinct colour so it stands out from a
  plain `read` or percentage at a glance.
- The toolbar button's popover states the same thing in words on a
  document page, for example "32% read · modified since you started".
- A directory page gets a "Mark all as read" action in its toolbar that
  marks every document in the listing. This is the baseline action: when
  the feature first appears every document is unread.

### Folder rollups

Folder entries show an unread count. The listing only knows direct
children, so the count comes from the site index, whose document entries
carry `version`. "Mark all as read" on a directory page then covers the
whole subtree. This step depends on the index and is last in the order
below; until then the action covers the listed documents only.

## Security

- `build` and default `serve` are unchanged: nothing new crosses the
  network.
- With sync enabled the server accepts one bounded, validated write that
  can only alter its own database. It cannot create paths, and routes
  that are not documents of the root are discarded.
- The database reveals which documents exist, when they were marked and
  how far they were read. By default it sits inside the served tree as a
  dotfile, which `serve` never lists or serves; its content is reachable only
  through the endpoint, beneath the path token. Because it is in the tree
  it also goes wherever the tree goes: a copy, an archive, a synced
  folder, a repository unless ignored. `--read-db` avoids that.
- Cross-site writes are rejected by the `Origin` check and the JSON
  content type; the shared CSP already limits `connect-src` to the same
  origin.

## Files touched

- `server.go`: version in `navEntry` and `pageData`, title cache storing
  the version, the sync wiring, the `read.json` routes, `readAccess`.
- `read_state.go` (new): record types, merge, validation, the SQLite
  store and its migrations.
- `go.mod`: `modernc.org/sqlite`.
- `build.go`: version per document, `version` in the site index.
- `main.go`: the `--read-sync` and `--read-db` flags.
- `templates/page.html`: `data-version`, `data-read-sync`, the toolbar
  button and popover, the end-of-article button, the directory action.
- `assets/app.js`: local store, merge, badges, controls, the position
  trail fed from `savePosition`, the chip, sync.
- `assets/cache.js`: exclude `read.json` from the worker cache.
- `assets/style.css`: badges, the button states, the popover lines.
- `README.md`: the feature, the two flags, the database file and a note
  to ignore it in version control.
- `THIRD_PARTY_NOTICES.md`: the SQLite driver.

## Implementation order

1. Document version in both modes: page, sidebar entries, table rows.
2. Local store, merge rule, toolbar button with popover and log, the
   end-of-article button. Usable in `build` and `serve` from here.
3. Listing badges for `read` and `read/modified`, and "Mark all as read"
   for the listed documents.
4. Position trail and progress: runs, collapsing, the percentage badges,
   the trail in the popover, the chip. Local to one device until the next
   step.
5. `serve --read-sync` and `--read-db`: the SQLite store, endpoint,
   validation, client sync of marks, trails and progress.
6. `version` in the site index, folder counts, subtree "Mark all as read".

## Tests

Go:

- the version value, its presence on pages, sidebar entries and table rows
  in `serve` and `build`, and its absence in `save` output;
- an unchanged version after a modification-time change, a changed one
  after a content change;
- the merge rules against the shared table: union, duplicates, order and
  cap for marks; the same run on both sides, order and the dwell-based
  dropping for trails;
- `read.json` absent without `--read-sync` or `--read-db`, present with
  either, and beneath the path token;
- `POST` rejections: wrong content type, missing or foreign `Origin`,
  oversized body, malformed records, routes outside the root or to
  missing documents;
- a `POST` round trip, the `ETag` and `304`, and two sequential writers
  converging;
- the database created at the root on first use, absent without the flag,
  never listed, never served and skipped by `build`;
- `--read-db` creating and using the file at the given path, relative and
  absolute, leaving the root untouched, and a file placed inside the tree
  staying unlisted and unserved;
- a startup error on a root, or a `--read-db` directory, that is not
  writable or does not exist;
- the `default` user created with the database, state written under it,
  two users' state kept apart at the storage layer, and a deleted user's
  rows removed by the cascade;
- a migration from an older `user_version`, and the refusal of a newer
  one;
- the progress merge: the larger `whole`, the earlier `start` and `base`,
  and a value older than the newest mark discarded.

Browser, manually with `playwright-cli` as in `CLAUDE.md`:

- mark a document as read, reload, and check the button, the sidebar
  marker and the stored log;
- edit the source and confirm the badge becomes `read/modified`;
- read a third of a document, confirm the percentage badge in the sidebar
  and the directory listing, skim to the end and confirm it does not
  change, then edit the source and confirm `/modified` is appended and
  stays while reading continues;
- mark as unread by accident, undo, and check the log;
- read slowly down a document and confirm the trail stays one run; jump to
  the end through the table of contents and back to the top, and confirm
  two short runs were added and the reading run is untouched;
- reopen the document and confirm the chip offers the reading run;
- at a different viewport width, follow a trail entry and confirm it lands
  in the same section;
- "Mark all as read" on a directory page;
- with `--read-sync`, mark in one browser profile and see it in another;
  read part of a document in one profile, open it in the other, and
  confirm it opens at the reading run;
  mark offline in both with different states and confirm the newer wins
  after both reconnect;
- the merge table evaluated in the page against the Go expectations.

## Non-goals

- Marking a document as read automatically from scrolling or time spent.
  The trail's dwell time informs what is offered and never changes a mark.
- Recording which parts of a document were covered. The trail holds
  positions, not ranges.
- Accounts, logins or cookies inside mdfmt. The schema is ready for
  several users; nothing creates or selects a second one.
- Share links and scoped access; see Relation to share links.
- Sharing read state between a `build` site and a `serve` instance, or
  between two origins without a server.
- Following a document through a rename or a move. Its record stays under
  the old route and the document shows as unread.
- Filtering or sorting listings by read state.

## Open questions

- A SQLite file inside a folder that a sync client mirrors between
  machines is the fragile spot of the default location. With the rollback
  journal and a single writer it is safe on one machine. Two machines
  serving their own synced copy and both writing will produce conflicted
  copies of the database, not a merge. `--read-db` with a path outside
  the synced folder is the answer for that setup; whether the default
  should warn when it detects one is open.
- Whether a custom database should record the root it was created for and
  refuse, or warn about, a different one. The plan documents the
  one-root rule and does not enforce it, since a moved folder would
  otherwise need a way to rebind.
- The pure Go SQLite driver adds several megabytes to an 18 MB binary.
  The alternative, the cgo driver, is smaller but ends cgo-free builds.
  The plan takes the size.
- The exact short form of the badges in the sidebar, where a long name
  and an age already compete for the width.
- Whether one tap on the toolbar button should toggle directly, with the
  log behind a secondary control, instead of opening the popover. The
  plan opens the popover and relies on the end-of-article button for the
  one-tap case.
- The trail's thresholds: the jump distances, the 30 minute gap, the 20
  second dwell for a reading run and the limit of 6 runs. They are first
  guesses to be tuned by use, and are constants in one place.
- Whether opening a document should prefer the newest reading run over
  this device's last position when the two differ. The plan restores the
  last position and offers the reading run in the chip, because jumping
  somewhere unexpected is worse than one extra tap.
- Whether the recent list's heading and pixel offset should be replaced by
  the trail's newest run, leaving one position store. The plan keeps both.
- Whether `read/modified` should become plain unread after some time, or
  stay distinct indefinitely. The plan keeps it distinct.
- Whether records of documents missing from the site index should be
  pruned from the local store. The server prunes on write; the client
  does not.
