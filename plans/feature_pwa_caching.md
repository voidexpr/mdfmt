# Feature: offline caching for the home-screen app

## Goal

Give the iOS home-screen app a graceful degraded experience without a
network connection:

1. keep a local copy of every page the reader has visited and serve it when
   the network is unavailable; when online, always serve the latest version
   from the server;
2. add a button on directory pages that caches every document below that
   folder, so a whole project can be taken offline from its root, or just a
   leaf folder.

This is for the site as installed on an iOS home screen, served over HTTPS
behind Caddy with an authentication layer in front. The mechanism is
standard web platform: a service worker with the Cache Storage API. It works
in ordinary browsers too, and on `serve` at `127.0.0.1`, which counts as a
secure context, so it can be developed and tested locally without a phone.

## Feasibility

Both steps are possible with what iOS Safari supports in standalone mode.
Service workers and Cache Storage have been available to home-screen web
apps since iOS 11.3, an installed app keeps its own storage separate from
Safari, and Apple exempts home-screen apps from the seven-day storage cap
applied to ordinary Safari sites. The relevant limits are:

- storage can still be evicted under disk pressure, so caches are
  best-effort and the UI must derive "is this cached" from the cache itself,
  never from a remembered flag;
- a service worker must be served from the site root to control the whole
  site, because its scope defaults to its own directory; placing it under
  `_mdfmt/` would need a `Service-Worker-Allowed` response header from Caddy;
- the auth layer decides what an unauthenticated fetch returns; the cache
  must never store a login page under a document's URL;
- cache storage is per origin and per scope, so a rotated path token leaves
  an orphaned cache behind unless the worker cleans up after itself.

None of these blocks the feature. The rest of this plan describes the
design.

## Service worker

`build` writes `sw.js` at the site root, next to the root `index.html`, and
`serve` serves the same embedded script at `/sw.js` beneath the path token.
The worker is a static asset with no templating: it derives everything from
`self.registration.scope`, which is the site root including any token.

- **Registration.** `app.js` registers `sw.js` relative to `data-root` when
  the page has a site root and `navigator.serviceWorker` exists. Standalone
  `save` files never register one.
- **Cache name.** One cache per site root, named from the scope path, for
  example `mdfmt:/TOKEN/`. On activation the worker deletes every other
  `mdfmt:` cache on the origin, which cleans up after a rotated token or a
  moved site.
- **Precache on install.** The shared assets under `_mdfmt/`, the manifest,
  and the offline fallback page, so the app shell exists after the first
  visit even if the reader never opens a second page.
- **Navigation and asset requests: network first, cache fallback.** Every
  same-origin GET goes to the network. A successful response is stored in
  the cache and returned. If the fetch fails, the cached copy is returned.
  If nothing is cached, navigations get the offline page and other requests
  fail as they do today. This satisfies "online always serves the latest";
  a served site's live rendering is never masked by a stale copy.
- **What gets cached.** Only same-origin responses with status 200 whose
  final URL equals the request URL (`response.redirected` false). An auth
  redirect to a login page therefore never poisons a document's cache
  entry. Query strings are stripped for the cache key, so `?raw=1` and the
  `?v=` asset versions do not create duplicate entries; raw responses are
  cached under their own key by including the query in that one case.
- **Slow networks.** A navigation waits for the network with a timeout of a
  few seconds before falling back to the cache when a cached copy exists,
  so a flaky connection does not leave the reader staring at a blank page.
  Without a cached copy the network request runs to completion.
- **Updates.** The worker calls `skipWaiting` and `clients.claim` so a new
  binary's worker takes over on the next load without a second reload. The
  worker script itself is fetched by the browser with its own update
  checks, which ignore HTTP caching beyond 24 hours, so a redeploy is picked
  up within a day even without cache headers.

## Offline fallback page

`build` writes `_mdfmt/offline.html` and `serve` serves it beneath
`.mdfmt/`. It is the page template rendered with a short message and an
empty article; `app.js` fills it at load time with the list of cached
documents, read from the cache and sorted most recent first using the
recent-documents records where available, so an offline reader can still
reach everything that was cached even if they land on an uncached URL.

## Online state in the UI

When a page was served from the cache the reader should know. A page
cannot read its own response headers, and `navigator.onLine` is not
reliable, so the worker posts an `offline` message to the client it served
a cached navigation to, and the page shows a small "Offline" pill in the
toolbar on that message and on the `offline` event, hiding it again on
`online`. The pill is informational only; navigation keeps working through
the cache.

## Cache a folder

The second step needs the list of URLs below a directory. The directory
page lists only its immediate children, so the site publishes one index:

- `build` writes `_mdfmt/site.json`: every page route, image route, and
  shared asset with its size in bytes, root-relative, plus a generation
  timestamp. It is regenerated on every build and precached by the worker.
- `serve` generates the same document at `/.mdfmt/site.json` on request by
  walking the served tree with the existing directory-listing rules,
  cached in memory with the same modification-time check the title cache
  uses.

The **cache button** appears in the toolbar of every directory page,
including the Projects hub, when a service worker controls the page. It
reads the index, filters entries whose route starts with the directory's
own route, and reports the count and total size before anything happens:
"Cache 42 files · 3.1 MB". Tapping it:

1. opens the site cache and fetches each URL not yet cached, a few at a
   time, storing successful responses with the same rules as the worker;
2. shows progress on the button itself ("17 / 42");
3. ends in a cached state showing the count and size, with a second action
   to remove the folder's entries from the cache.

The work runs in the page, not the worker, because the Cache Storage API is
available to windows and progress reporting is then trivial. Leaving the
page mid-way simply stops the loop; the next visit recomputes the state
from the cache and offers to finish. Documents already cached by visiting
them count as done. Referenced images are included because the index lists
them per page; the shared assets are already precached.

The button is hidden when the page has no site root, when no worker
controls the page, and while the cache index is unavailable, so ordinary
`save` output and first loads before the worker activates show nothing.

## Cache freshness for pre-cached documents

A document cached by the folder button but never visited is served from the
network when online like any other page, and the fresh response replaces
the cached one. Nothing expires on its own; the site index carries a
generation timestamp, and after a rebuild the worker's activation compares
it with the cached copy and refreshes the entries of any cached folder in
the background when online. This keeps "cache this folder" meaning "keep
this folder available offline", not "freeze it".

## `serve` specifics

`serve` renders on request, so its cached pages go stale faster than a
build's, but network-first hides that whenever the server is reachable.
The `sw.js` route and `.mdfmt/site.json` require the path token like every
other route. Editor forms are POSTs and bypass the worker. Raw and image
responses are cached under their own keys.

## Security

- The worker script and the registration are same-origin; `worker-src`
  falls back to `script-src 'self'`, so the current Content-Security-Policy
  already allows it. Responses served from the cache carry the original
  headers, including the CSP header from `serve`.
- Nothing is cached across origins, and nothing is cached from redirected
  responses, so the auth layer's pages never enter the cache.
- The cache lives in the installed app's private storage on the device.
  Removing the app removes it. The site index reveals the same routes the
  directory pages already expose.

## Home-screen resume while offline

The cold-launch redirect keeps working: the root page is served from the
cache, `app.js` redirects to the last document read, and that document is
in the cache because it was visited. If the latest record points at a
document that was never cached, the offline page shows the cached list
instead.

## Files touched

- `assets/sw.js`: the worker, embedded like the other assets.
- `assets/app.js`: registration, the offline pill, the offline page list,
  the folder cache button.
- `assets/style.css`: the pill and the button states.
- `templates/page.html`: the button in the directory toolbar, the pill
  placeholder in the metadata row, the offline page body.
- `server.go`: routes for `/sw.js`, `.mdfmt/site.json`, `.mdfmt/offline.html`;
  the in-memory site index.
- `build.go`: `sw.js` at the site root, `_mdfmt/site.json`,
  `_mdfmt/offline.html`; the index is a by-product of the inventory the
  build already keeps.
- `README.md`: an offline section covering what is cached, the folder
  button, the storage caveats, and a note that a stable `--path-token` is
  preferable for an installed app so the cache survives rebuilds.

## Implementation order

1. The worker with precache, network-first fallback, redirect guard and
   cache cleanup; `sw.js` emitted by `build` and served by `serve`;
   registration in `app.js`. This alone delivers step one.
2. The offline fallback page and the cached-documents list on it.
3. The online-state pill.
4. The site index in `build` and `serve`.
5. The folder cache button with progress, cached state and removal.
6. Background refresh of cached folders after a rebuild.
7. README, tests, screenshots if the toolbar changes visibly on desktop.

## Tests

Go tests, using `httptest` and temporary directories:

- `sw.js` at the build root and at `/TOKEN/sw.js` in `serve`, with
  `text/javascript` and, in `serve`, a `Service-Worker-Allowed`-free
  response since the scope is already the root;
- `_mdfmt/site.json` listing every page, image and asset route with sizes,
  root-relative, no token, regenerated per build;
- `serve`'s site index reflecting a file added after the first request;
- the offline page present in both modes and linked nowhere but the worker;
- the folder button present on directory pages and absent from documents
  and standalone output;
- unchanged CSP.

Browser checks with playwright-cli against `serve` on `127.0.0.1`:

- register the worker, visit two documents, set the context offline, reload
  each and confirm the cached copies render;
- offline navigation to an unvisited document lands on the offline page
  listing the two cached ones;
- back online, edit a file and reload to confirm the live version wins;
- cache a folder, confirm the count, go offline and open an unvisited
  document from it;
- remove the folder cache and confirm the offline page returns for it;
- a redirected response is not cached.

On the phone: install the app, browse, enable Airplane Mode, relaunch from
the home screen, and confirm the resume lands on the last document.

## Non-goals

- Background sync or push notifications.
- Syncing cache selections between devices.
- Offline editing or offline `serve` editor actions.
- Caching across path tokens; a rotated token is a new site.
- Making the cache a substitute for the auth layer's session handling; if
  the session expires while offline, cached pages still display, and the
  next online request goes through the auth layer as usual.

## Open questions

- Whether the network-first timeout for navigations should be user-visible
  or fixed at a few seconds. The plan fixes it.
- Whether to cap the total cache size or the folder size before caching.
  Sizes are shown before caching, so the plan leaves the decision to the
  reader.
