// Offline support for the home-screen app. Every same-origin request goes to
// the network first and its successful response replaces the cached copy, so
// an online reader always sees the server's latest page. Only a failed fetch
// falls back to the cache, then to the offline page for navigations.
//
// The script is static: the site root is the registration scope and the
// asset directory arrives in the registration URL's query, so the same file
// serves a static build (_mdfmt/) and a served tree (.mdfmt/). cache.js is
// prepended at embed time and provides mdfmtCache.
const scope = new URL(self.registration.scope);
const cacheName = mdfmtCache.name(scope.pathname);
const cachePromise = caches.open(cacheName);
const assets = new URL(new URLSearchParams(self.location.search).get("assets") || "_mdfmt/", scope);
const offlineURL = new URL("offline.html", assets).href;
// The app shell, precached on install. Keep in step with writeAssets in
// build.go and serveAsset in server.go.
const shell = [
  "style.css",
  "app.js",
  "manifest.webmanifest",
  "offline.html",
  "favicon.svg",
  "favicon.ico",
  "favicon-16.png",
  "favicon-32.png",
  "favicon-48.png",
  "apple-touch-icon.png",
].map((name) => new URL(name, assets).href);
const navigationTimeout = 4000;

// Tells the page that opened from a cached copy, once it exists as a client;
// navigator.onLine is not reliable enough to show the offline state.
const notifyOffline = async (clientId) => {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const client = await self.clients.get(clientId);
    if (client) {
      client.postMessage({ type: "offline" });
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
};

const networkFirst = async (event) => {
  const { request } = event;
  const navigate = request.mode === "navigate";
  const cache = await cachePromise;
  const key = mdfmtCache.key(request.url);
  // Only a navigation needs the cached copy up front, to decide the timeout.
  let cached = navigate ? await cache.match(key) : undefined;
  try {
    // A flaky connection should not leave a reader with a cached copy
    // staring at a blank page.
    const response = await fetch(request, cached ? { signal: AbortSignal.timeout(navigationTimeout) } : {});
    if (mdfmtCache.cacheable(response)) event.waitUntil(cache.put(key, response.clone()));
    return response;
  } catch (error) {
    if (!navigate) cached = await cache.match(key);
    if (cached) {
      if (navigate) event.waitUntil(notifyOffline(event.resultingClientId));
      return cached;
    }
    if (navigate && (await cache.match(offlineURL))) {
      // Redirect rather than serve the page in place, so its relative links
      // resolve from the offline page's own location.
      const from = new URL(request.url).pathname.slice(scope.pathname.length);
      return Response.redirect(`${offlineURL}?from=${encodeURIComponent(from)}`, 302);
    }
    throw error;
  }
};

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await cachePromise;
      await Promise.allSettled(shell.map((url) => cache.add(url)));
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      // A rotated path token or a moved site leaves an orphaned cache behind.
      const prefix = mdfmtCache.name("");
      for (const name of await caches.keys()) {
        if (name.startsWith(prefix) && name !== cacheName) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== scope.origin || !url.pathname.startsWith(scope.pathname)) return;
  event.respondWith(networkFirst(event));
});
