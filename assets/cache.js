// The offline cache protocol shared by the page script and the service
// worker; server.go prepends this file to both app.js and sw.js so the two
// sides agree on the cache name, the key of a URL, and what may be stored.
const mdfmtCache = {
  // One cache per site root, named from its path.
  name: (rootPath) => `mdfmt:${rootPath}`,

  // Asset versions and view parameters share one entry; only the raw source
  // view is a distinct resource. The path's percent-encoding is normalised
  // so a typed address and the server's own links share one entry.
  key: (url) => {
    const key = new URL(url);
    if (key.searchParams.get("raw") !== "1") key.search = "";
    try {
      key.pathname = decodeURIComponent(key.pathname);
    } catch {
      // A malformed escape stays as it is.
    }
    return key.href;
  },

  // A redirected response would store the auth layer's login page under a
  // document's URL.
  cacheable: (response) => response.status === 200 && !response.redirected && response.type === "basic",
};
