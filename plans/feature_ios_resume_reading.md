# Feature: resume reading, recent documents, and a mobile table of contents

## Goal

Make a deployed `mdfmt build` site comfortable to read on a phone, in
particular as an iOS home-screen web app:

- every document remembers its reading position and reopens there;
- a home-screen launch lands on the last document read, at its position;
- a toolbar menu lists the 20 most recently viewed documents, most recent
  first, and opening one restores its position, so switching between a few
  files loses no context;
- a toolbar button shows the document's table of contents on narrow screens,
  where the right sidebar is hidden today.

Everything is client-side behaviour in the shared `assets/app.js` and
`assets/style.css`, plus a static web manifest, one Content-Security-Policy
directive, and small template additions. No server state is introduced and
`build` stays the deployment target. `serve` and `save` share the assets, so
they gain the same behaviour where it applies.

## Deployment context

- The static site is published behind Caddy, which terminates TLS and layers
  authentication on top. HTTPS is what makes home-screen installation
  possible, so the manifest work applies to `build` output only in practice.
- iOS gives an installed home-screen app its own cookie jar and
  `localStorage`, separate from Safari. The user signs in once inside the
  app; saved positions live there too. The auth layer should issue a
  long-lived session, and cookie-based auth fits better than HTTP basic auth,
  whose modal prompt repeats in standalone mode.
- If the auth flow redirects through a login page, the `?resume=1` marker on
  the manifest's start URL may not survive the round trip. The standalone
  fallback described below covers that case.
- In `display: standalone` mode there is no address bar and no back button.
  The recent-documents menu and the table-of-contents button are the primary
  navigation on the phone, not conveniences.
- `--path-token auto` rotates the token on every rebuild. Stored URLs are
  therefore relative to the site root, never absolute paths.

## Site root and page identity

Every generated page already computes relative asset URLs from its depth.
Add a `RootURL` field to `pageData`, the relative URL of the site root from
the current page (`./`, `../`, `../../`, ...). The page template renders it as
`<html lang="en" data-root="{{.RootURL}}">`.

- In `build` the site root is `TARGET_DIR` or `TARGET_DIR/TOKEN`.
- In `serve` it is `/` or `/TOKEN/`.
- `save` standalone files have no site; `standalone.html` renders no
  `data-root`.

The script resolves `data-root` against `location.href` to obtain the
absolute root, and identifies a document by its path relative to that root,
for example `guide/setup.html` in `build` or `guide/setup.md` in `serve`.
Without `data-root`, the identity is `location.pathname`.

## Reading positions and the recent list

One `localStorage` entry, key `mdfmt.recent`, holds a JSON array of at most
20 records ordered most recent first:

```json
[
  {
    "url": "guide/setup.html",
    "title": "Setup",
    "heading": "installing-the-binary",
    "offset": 42,
    "updated": 1789000000000
  }
]
```

- `url` is the root-relative document path as defined above.
- `title` is the document title already rendered in the page.
- `heading` is the ID of the last heading whose top is at or above the
  toolbar line (the same rule the current-heading highlight already uses),
  or empty when the document has no headings.
- `offset` is the distance in pixels from that heading's top to the current
  scroll line, or the raw `scrollY` when there is no heading. Heading-relative
  positions survive rebuilds that change content above the reading position.
- `updated` is a millisecond timestamp.

Only document pages write records. Directory pages, the collection hub, and
raw or image views never do, because they have no reading position and the
home-screen launch must land on a file.

A document page updates its own record, moving it to the front of the array
and trimming to 20:

- on `DOMContentLoaded`, so the document is at the front even before the
  reader scrolls;
- on scroll, debounced to roughly 250 ms, because iOS may terminate a
  suspended app without running any final handler;
- on `visibilitychange` to `hidden` and on `pagehide`, the reliable moments
  when switching apps or navigating away.

All storage access is wrapped in `try`/`catch`; when storage is unavailable
the page behaves as it does today. Records are bounded by count, not age; a
document deleted from the site simply produces a 404 when opened from the
menu, which the menu's clear action or the natural rotation of 20 entries
takes care of.

## Position restore

On `DOMContentLoaded` a document page restores its saved position when all
of the following hold:

- the URL carries no `#fragment`; a fragment means the reader followed a link
  to a specific heading;
- the navigation type reported by `PerformanceNavigationTiming` is
  `navigate`; on `reload` and `back_forward` the browser restores scroll by
  itself and must not be fought (when the API is unavailable, treat the load
  as `navigate`);
- a record for this document exists.

Restoring scrolls instantly (no smooth behaviour) to the saved heading's
current top plus `offset`, or to the raw offset without a heading, clamped to
the document height. This single rule covers a tap in the recent menu, a tap
in the left sidebar, a bookmark, and the home-screen launch. The existing
"top" link still scrolls to the top and its handler also resets the record's
position to the top.

## Recent documents menu

A button `data-recent-toggle` joins `.page-actions` in the `main` template,
after the theme button, followed by an empty `<div class="page-menu"
data-recent-menu hidden>`. The button is rendered on every page that has
`data-root`, including directory pages, so the reader can jump back to a
file from anywhere. `standalone.html` omits it.

The script:

- hides the button when the list is empty or storage is unavailable;
- fills the menu on open from the stored list, excluding the current
  document, one link per record showing the title and a compact relative
  age using the existing `relativeAge` helper, plus a final "Clear" action
  that empties the list;
- links resolve as `root + record.url`, so they stay valid after a token
  rotation as long as the current page's root is current;
- closes on Escape, on a click outside, and after choosing an entry.

The two toolbar menus (recent and table of contents) share one small
popover mechanism: a single open popover at a time, `aria-expanded` on the
toggle, `hidden` on the panel.

## Table of contents on narrow screens

The right sidebar already contains the table of contents and the "top" link
and is hidden below 900 px. Rather than duplicating markup, a toolbar button
`data-toc-toggle`, rendered only when `.TOC` is non-empty, toggles the
existing sidebar into an overlay:

- the button is hidden by CSS at widths where the sidebar is visible;
- when open, `.right-sidebar` is displayed as a fixed panel anchored below
  the toolbar, full width on phones, scrollable, above the content; a
  backdrop click or Escape closes it;
- choosing a heading closes the panel; the browser then jumps to the
  fragment, and the current-heading highlight keeps working from scroll;
- `standalone.html` includes `toc-sidebar`, so it gets the same button and
  behaves identically.

The left sidebar (directory navigation) stays hidden on narrow screens.
Breadcrumbs and the recent menu cover navigation there; a matching
file-list button is a possible later addition and is out of scope.

## Home-screen manifest and cold-launch resume

`build` writes `_mdfmt/manifest.webmanifest` next to the other shared assets
and `serve` serves the same content at `.mdfmt/manifest.webmanifest`. The
`favicons` template links it with `<link rel="manifest" href="...">` using
the same relative asset URL scheme; `standalone.html` does not link it.

Manifest content, identical for both modes since URLs resolve relative to
the manifest file:

```json
{
  "name": "mdfmt",
  "short_name": "mdfmt",
  "start_url": "../index.html?resume=1",
  "display": "standalone",
  "icons": [
    { "src": "apple-touch-icon.png", "sizes": "180x180", "type": "image/png" }
  ]
}
```

For `serve` the start URL is `../?resume=1`, because served directory URLs
end with a slash. The default scope is the start URL's directory, that is
the site root, so the whole site stays inside the app. The token never
appears in the manifest.

Cold-launch resume runs in the head-time part of `app.js`, the same place
that applies the theme before the body is parsed, and only on the site root
page (the page whose path equals the resolved root). A cold launch is:

- the `resume` query parameter being present, or
- `navigator.standalone === true` with `history.length === 1`, which
  covers a marker lost through an auth redirect.

On a cold launch with a non-empty list, the script calls
`location.replace(root + records[0].url)`. Running in the head aborts the
current load before the directory page renders, so the visible cost is one
extra request. Without a record, or when not a cold launch, the marker is
stripped with `history.replaceState` so it never leaks into later URLs.
Restricting the check to the root page also prevents the standalone
heuristic from firing again on the document the launch redirected to.
Regular browsers and bookmarks are unaffected: `/` without the marker keeps
opening the root directory page.

## Security policy and cache busting

- `contentSecurityPolicyWithForm` gains `manifest-src 'self'`, which both
  the `serve` header and the static build meta tag use. `standaloneCSP` is
  unchanged.
- The popovers and the overlay use no inline styles or scripts, so
  `style-src` and `script-src` stay `'self'`.
- `serve` bumps the `?v=` query on `app.js` and `style.css`.
- Storage keys stay under the existing `mdfmt.` prefix: `mdfmt.recent`
  joins `mdfmt.theme` and `mdfmt.directory-sort`.

## Files touched

- `assets/app.js`: site root resolution, the record store, save and restore,
  the popover mechanism, the recent menu, the table-of-contents toggle, the
  cold-launch redirect.
- `assets/style.css`: toolbar buttons, popover panel, narrow-screen overlay
  for `.right-sidebar`.
- `templates/page.html`: `data-root` on `<html>`, the manifest link, the two
  buttons and the menu container in the toolbar.
- `templates/standalone.html`: the table-of-contents button only.
- `server.go`: `RootURL` in `pageData`, the manifest asset route, the CSP
  directive, version bumps.
- `build.go`: `RootURL` per page, the manifest in the shared asset list.
- `README.md`: a short section on reading positions, the recent menu, and
  installing the site as a home-screen app behind an authenticating proxy.
- `docs/file.png` and `docs/docs.png`: retaken, since the toolbar changes.

## Non-goals

- Syncing positions or the recent list between devices; the store is per
  device and per installed app.
- Service worker or offline reading.
- Expiring records by age.
- A file-list panel on narrow screens.
- Any server-side or per-user state.

## Implementation order

1. Record store, save on the four triggers, and the restore rule in
   `app.js`, with `data-root` in the templates and `RootURL` in Go.
2. Popover mechanism, recent menu button and panel, styling for phones.
3. Table-of-contents button and the narrow-screen overlay.
4. Manifest asset in `build` and `serve`, the manifest link, the CSP
   directive, the cold-launch redirect, version bumps.
5. Tests, README, screenshots.

## Tests

Go tests, using `httptest` for `serve` and temporary directories for `build`,
cover at least:

- `data-root` values on root and nested pages in `build`, with and without a
  token, and in `serve`;
- no `data-root` and no recent button in `save` output;
- the recent button and menu container on document and directory pages;
- the table-of-contents button present only when a document has headings,
  in `page.html` and `standalone.html`;
- the manifest written by `build` under `_mdfmt/`, its relative start URL,
  and the absence of any token in it;
- the manifest route in `serve` with the correct content type, requiring the
  path token when tokens are enabled, and its start URL ending in `/?resume=1`;
- the manifest link on generated pages and its absence in standalone files;
- `manifest-src 'self'` in the `serve` header and the static meta tag, and
  unchanged `standaloneCSP`;
- the bumped asset versions.

The browser behaviour has no automated harness in this repository. Verify it
manually with `playwright-cli` against a `serve` instance, following the CDP
attach notes in `CLAUDE.md`:

- open a document, scroll, dispatch `pagehide`, and check the stored record's
  `heading` and `offset`;
- navigate to the same document afresh and confirm the scroll position;
- open it with a fragment and confirm the fragment wins;
- reload and confirm the browser's own restoration is not overridden;
- view three documents and check the menu order and the exclusion of the
  current page;
- load `ROOT/?resume=1` and confirm the redirect to the latest document and
  the stripped marker when the list is empty;
- at a 400 px viewport, open the table of contents, choose a heading, and
  confirm the panel closes and the page jumps.
