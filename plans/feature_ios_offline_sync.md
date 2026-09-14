# Feature: automatic offline sync of the site

## Goal

Treat the whole site as a set of articles to keep for later, the way a
read-it-later app does: the installed home-screen app downloads new and
changed pages by itself, removes pages that were deleted on the server, and
never refetches what it already has. The reader taps nothing after choosing
what to keep.

This builds on the offline caching feature (`plans/feature_pwa_caching.md`),
which already provides the service worker, network-first serving with cache
fallback, the offline page, the "Offline" pill, a site index and a folder
cache button. That feature caches on demand: a page is stored when visited
or when the reader taps the button, and it is refreshed only when visited
again. This plan replaces the one-off button with a subscription and adds
one reconcile pass that keeps the cache current.

## What the platform allows

The web manifest cannot preload anything; there is no manifest member that
lists URLs. All offline content goes through the service worker and Cache
Storage, and on iOS a web app runs only in the foreground: Periodic
Background Sync and Background Fetch are Chromium-only, and a push
notification cannot make the worker fetch article bodies. "Automatic"
therefore means "on every launch and page load while the app is open and
online", which is enough: opening the app once brings the mirror up to
date, and reading keeps it fresh.

## Design summary

1. The site index gains a per-entry stamp that changes whenever the served
   bytes of that route change.
2. The folder cache button becomes a subscription: tapping it on a
   directory means "keep this folder available offline", and on the root
   page "keep the whole site". Subscriptions persist on the device.
3. On every page load while online, after the worker is ready, the page
   runs one reconcile pass against the index: fetch entries of subscribed
   folders that are missing or whose stamp differs, refresh changed pages
   that are cached for any other reason, and delete cached pages the index
   no longer lists. The pass is cheap when nothing changed and resumable
   when interrupted.

Everything else stays as implemented: network-first serving, the redirect
guard, the offline page, cache cleanup on token rotation, and the state of
the button always derived from the cache itself.

## Why the index rather than crawling directory pages

Directory pages already carry a name, modification stamp and size per row,
so a crawl from the root could discover the tree, detect changes and infer
deletions from listing absence without any index. Both approaches were
considered. The index wins on three points:

- **Template and style changes.** A rebuild that changes the page chrome
  without touching any source keeps every source stamp equal, so a
  listing-driven crawl would never refresh the mirrored HTML. A build
  stamps index entries with a hash of the output bytes, which catches this
  case without a separate generation marker.
- **One consistent snapshot.** The index is produced in one walk, so a pass
  never sees a half-renamed tree. Directory pages fetched over several
  seconds can disagree with each other while files are being edited.
- **No HTML parsing on the client.** The crawl would need the DOM parser,
  which only the page has, and the row markup would become an interface.
  The index already is one, and it already lists each page's images in a
  build.

The crawl keeps one advantage, no new server work, but the index exists
already. If the index were ever dropped, the crawl is the fallback design
and this plan's client side works the same way over it.

## Site index stamps

Each `entries[]` item gains a `stamp` string, opaque to the client:

- **`build`** uses a short prefix of the SHA-256 of the written file, for
  pages, images and assets alike. The index is written after every other
  file, so hashing happens in the same walk that collects sizes. Assets are
  content-versioned already through their `?v=` query; the stamp merely
  unifies the rule.
- **`serve`** cannot hash rendered output without rendering every document
  on each index request. A document's stamp is its source modification
  time and size, the same pair the title cache uses to detect changes. A
  directory page's stamp is a hash over its children's names, sizes and
  modification times, which the listing walk already visits. A change in
  the binary's rendering is not reflected; network-first hides that
  whenever the server is reachable, and the next source edit refreshes the
  page.
- **Generation.** The existing `generated` field becomes meaningful for
  skipping work: `serve` sets it to the newest modification time seen in
  the walk rather than the request time, so it changes only when the tree
  does. A pass that finds `generated` equal to the value stored after the
  last completed pass ends immediately.

`serve` walks the tree on every index request today. With a request per
page load the walk should be memoised for a few seconds, keyed on nothing
but time; a stale index within that window only delays a refresh to the
next page load.

The served index still lists no images. The reconcile finds them by
scanning each stored document's HTML for same-origin `<img>` sources, which
also covers `build` output uniformly; the `images` field remains a shortcut
that avoids the scan when present.

## Stored stamps

The stamp of a cached entry lives with the entry: the reconcile stores the
response itself with an added `X-Mdfmt-Stamp` header rather than relying on
the worker's put. This keeps one source of truth: an entry evicted by the
device disappears together with its stamp, and a page cached by an ordinary
visit has no stamp and is refreshed by the next pass, which then records
one. No second store in `localStorage` or IndexedDB can drift from the
cache.

The worker keeps storing every successful network response on ordinary
navigation, so a visited page is always fresh when online whether or not
the reconcile has run.

## Subscriptions

The folder cache button keeps its place in the toolbar of every directory
page, but its meaning changes from an action to a switch:

- **Not subscribed**: "Keep offline · 42 · 3.1 MB". Tapping it records the
  directory's route in the subscription list and starts a pass immediately,
  with progress on the button as today ("17 / 42").
- **Subscribed and complete**: "Offline · 42 · 3.1 MB". Tapping it asks for
  confirmation, removes the subscription and deletes the folder's entries
  from the cache, except pages that a subscription higher up still covers.
- **Subscribed and incomplete**, for example after an interrupted pass or an
  eviction: the button shows the count still missing and a tap resumes.

The list is stored in `localStorage` under `mdfmt.offline.folders`, next to
the recent-documents list, as root-relative routes. The root page's button
subscribes the whole site with route `""`. A folder inside a subscribed
ancestor shows as subscribed and cannot be unsubscribed on its own; the
button's title says which ancestor covers it. Subscriptions are per site
root like the cache; a rotated path token starts empty, as the README
already advises for installed apps.

There is no default subscription. Sizes are shown before the first tap, and
a reader who wants the read-it-later behaviour taps the root button once.
The README recommends exactly that for the home-screen app.

## The reconcile pass

The pass runs in the page, not the worker, for the same reasons as the
existing button: the Cache Storage API is available to windows, progress
is trivial to show, and a page outlives the worker's idle timeout. It
starts after `navigator.serviceWorker.ready` on every page load with a site
root, when `navigator.onLine` is true, and never on the offline page. It
is skipped without a fetch when a pass is already running in another tab,
detected with a short-lived `localStorage` lock, and ends immediately when
the index's `generated` value equals the stored one.

Otherwise, with the index in hand:

1. **Plan.** Build the set of wanted routes: every page and image entry
   whose route starts with a subscribed folder, plus the images referenced
   by those pages. Read the cache keys once. For each wanted route, decide
   between skip (cached with an equal stamp), fetch (missing or different
   stamp) and, for cached routes outside every subscription, refresh only
   when the stamp differs. Cached page routes that the index does not list
   at all are deleted; this is how a document removed on the server leaves
   the device. Assets under the asset directory and the offline page are
   never deleted by the pass; the worker owns them.
2. **Order.** Directory pages first, so offline navigation works as early
   as possible, then documents newest first by the index order `build` and
   `serve` already produce, then images. An interrupted pass leaves the
   most useful subset.
3. **Fetch.** Four workers drain the queue, as today. A response is stored
   only under the worker's own rule: status 200, not redirected, same
   origin. A failed fetch leaves the entry for the next pass. Each stored
   response carries its stamp header.
4. **Finish.** Store the index's `generated` value and refresh the button
   state from the cache. If any fetch failed, the value is not stored, so
   the next page load retries.

Leaving the page mid-pass stops the loop; nothing is half-written because
each `cache.put` is atomic per entry, and the next load recomputes the plan
from the cache. Being sent to the background on iOS is the same case.

A pass over an unchanged site costs one small index request per page load.
A rebuild that changes ten pages costs ten fetches. A first subscription to
a site of a thousand text pages is on the order of ten megabytes and
finishes in the background while the reader keeps reading.

## Size policy

The index carries sizes, so the pass can stay within a budget: images
larger than a threshold, initially 2 MB, are not mirrored automatically and
are cached only when viewed. The button's size summary excludes them and
says so ("3.1 MB + 2 large images"). No total cap is applied; the summary
is the reader's information for deciding what to subscribe to.

## UI

- The button texts above; the "syncing" state uses the progress label the
  button already has.
- The offline page keeps listing cached documents most recently read first,
  and now groups subscribed folders above pages that were merely visited,
  so an offline reader sees what is guaranteed to be complete.
- The "Offline" pill is unchanged.

## `serve` specifics

The served index gains the stamps and the memoisation above. Its walk
already applies the listing rules, so hidden and excluded files are never
mirrored. Editor POSTs bypass the worker and the pass. A file saved through
the editor changes its stamp and is refreshed on the next page load.

## Security

Unchanged from the caching plan. The stamp header is added to stored
responses only; nothing new crosses the network. The index reveals the same
routes as before, plus stamps derived from modification times and sizes,
which the directory pages already show.

## Files touched

- `server.go`: `stamp` on `siteEntry`; directory stamps and the newest
  modification time in `siteIndex`; short memoisation of the encoded index.
- `build.go`: output hashes as stamps in `writeSiteIndex`.
- `assets/app.js`: the subscription list; the reconcile pass replacing the
  one-off fetch loop of the folder button; the button states; the offline
  page grouping.
- `assets/style.css`: the incomplete button state, if visually distinct.
- `templates/page.html`: button title text for the covered-by-ancestor case.
- `README.md`: rewrite the folder button paragraph as "keep offline",
  describe the automatic refresh and deletion, the large-image threshold,
  and the recommendation to subscribe the root on the phone.

## Implementation order

1. Stamps in both indexes with tests; the client ignores them at first.
2. The reconcile pass, driven by the existing button as a one-off, storing
   stamps and deleting unlisted pages. This already delivers "only new and
   changed, deleted goes away" for a folder the reader re-taps.
3. Subscriptions and the pass on every page load, with the `generated`
   short-circuit and the tab lock.
4. Image discovery from stored HTML for `serve`, and the size threshold.
5. Offline page grouping, README, screenshots if the button text changes
   the desktop toolbar visibly.

## Tests

Go tests:

- every index entry has a non-empty `stamp`; a build's stamp changes when
  the rendered output changes, including a template-only change with
  unchanged sources, and stays equal across two builds of unchanged input
  with a stable path token;
- `serve` stamps change on a source edit and on adding or removing a child
  of a directory, and `generated` is the newest modification time seen;
- the memoised index is refreshed after its window.

Browser checks with playwright-cli against `serve` on `127.0.0.1`:

- subscribe a folder, confirm every page below it is cached with a stamp;
- edit one file, reload any page, confirm only that page is refetched;
- delete a file, reload, confirm its cache entry is gone and the offline
  page no longer lists it;
- add a file to the folder, reload, confirm it is fetched;
- reload with nothing changed and confirm the index is the only request
  beyond the page's own;
- interrupt a pass by navigating away, confirm the next load resumes and
  the button shows the remaining count meanwhile;
- unsubscribe and confirm the entries are removed unless covered by a root
  subscription.

On the phone: subscribe the root, close the app, edit and delete files on
the server, reopen the app online for a few seconds, enable Airplane Mode
and confirm the edits are visible and the deleted page is absent.

## Non-goals

- Anything that runs while the app is closed.
- Syncing subscriptions between devices.
- Offline edits and their replay; the editor stays online-only.
- Mirroring files the directory listing rules hide.

## Open questions

- Whether the served index should hash source content instead of using the
  modification time and size pair. The title cache already opens every
  file, so a hash is nearly free, but the pair is enough for a personal
  site and matches the existing invalidation rule. The plan keeps the pair.
- Whether the large-image threshold should be a setting. The plan fixes it
  and shows the excluded count.
