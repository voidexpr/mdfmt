(() => {
  const root = document.documentElement;
  const themeKey = "mdfmt.theme";
  const themeModes = ["system", "light", "dark"];
  const themeLabels = {
    system: "Theme: follows system",
    light: "Theme: light",
    dark: "Theme: dark",
  };

  const savedTheme = () => {
    try {
      const value = localStorage.getItem(themeKey);
      return themeModes.includes(value) ? value : "system";
    } catch {
      return "system";
    }
  };

  const applyTheme = (mode) => {
    if (mode === "system") root.removeAttribute("data-theme");
    else root.dataset.theme = mode;
    for (const button of document.querySelectorAll("[data-theme-toggle]")) {
      button.dataset.mode = mode;
      button.title = themeLabels[mode];
      button.setAttribute("aria-label", themeLabels[mode]);
    }
  };

  // Runs before the body is parsed so the page never paints in the wrong theme.
  applyTheme(savedTheme());

  // A JSON setting kept in localStorage with a path-wide cookie fallback,
  // so it survives when one of the two is unavailable.
  const persistedSetting = (storageKey, cookieKey) => ({
    load: () => {
      try {
        const value = JSON.parse(localStorage.getItem(storageKey));
        if (value) return value;
      } catch {
        // Try the cookie below.
      }
      try {
        const prefix = `${cookieKey}=`;
        const cookie = document.cookie
          .split(";")
          .map((part) => part.trim())
          .find((part) => part.startsWith(prefix));
        if (!cookie) return null;
        const serialized = decodeURIComponent(cookie.slice(prefix.length));
        const value = JSON.parse(serialized);
        localStorage.setItem(storageKey, serialized);
        return value;
      } catch {
        return null;
      }
    },
    save: (value) => {
      const serialized = JSON.stringify(value);
      try {
        localStorage.setItem(storageKey, serialized);
      } catch {
        // Fall through to the path-wide cookie.
      }
      try {
        document.cookie =
          `${cookieKey}=${encodeURIComponent(serialized)}; Path=/; ` +
          "Max-Age=31536000; SameSite=Lax";
      } catch {
        // The setting still applies to this page when storage is disabled.
      }
    },
  });

  // The left sidebar collapses to a narrow rail; the state is applied here
  // as well so the layout never paints expanded first.
  const navSetting = persistedSetting("mdfmt.sidebar", "mdfmt_sidebar");
  const applyNavCollapsed = (collapsed) => {
    root.classList.toggle("nav-collapsed", collapsed);
    const label = collapsed ? "Show the sidebar" : "Hide the sidebar";
    for (const button of document.querySelectorAll("[data-nav-toggle]")) {
      button.setAttribute("aria-expanded", String(!collapsed));
      button.title = label;
      button.setAttribute("aria-label", label);
    }
  };
  applyNavCollapsed(navSetting.load() === "collapsed");

  // Reading positions live in one list of the most recently viewed documents.
  // The page announces its root page (the directory page in serve, index.html
  // in a build). A document is identified by its path relative to the root
  // directory, so records survive a rotated path token or a moved site.
  const recentKey = "mdfmt.recent";
  const recentLimit = 20;
  const rootPage = root.dataset.root === undefined ? null : new URL(root.dataset.root, location.href);
  const siteRoot = rootPage && new URL("./", rootPage);
  // A site route is a path relative to the site root, or null outside it.
  const siteRoute = (url) =>
    siteRoot && url.pathname.startsWith(siteRoot.pathname) ? url.pathname.slice(siteRoot.pathname.length) : null;
  const identity = siteRoute(location) ?? location.pathname;

  const loadRecent = () => {
    try {
      const list = JSON.parse(localStorage.getItem(recentKey));
      return Array.isArray(list) ? list.filter((record) => typeof record?.url === "string") : [];
    } catch {
      return [];
    }
  };
  const storeRecent = (list) => {
    try {
      localStorage.setItem(recentKey, JSON.stringify(list.slice(0, recentLimit)));
    } catch {
      // Positions are best-effort when storage is unavailable.
    }
  };
  const recordURL = (record) => (siteRoot ? new URL(record.url, siteRoot).href : record.url);

  // The service worker keeps visited pages for offline reading. It lives at
  // the site root and learns the asset directory from its registration URL.
  // cache.js, prepended at embed time, provides mdfmtCache for both sides.
  const cacheName = siteRoot && mdfmtCache.name(siteRoot.pathname);
  const scriptURL = document.currentScript?.src;
  const assetDir = scriptURL ? siteRoute(new URL("./", scriptURL)) : null;
  if (assetDir && "serviceWorker" in navigator) {
    navigator.serviceWorker
      .register(new URL(`sw.js?assets=${encodeURIComponent(assetDir)}`, siteRoot), { scope: siteRoot.href })
      .catch(() => {
        // Offline support is best-effort; the site works without it.
      });
    navigator.serviceWorker.addEventListener("message", (event) => {
      if (event.data?.type === "offline") dispatchEvent(new Event("mdfmt:offline"));
    });
  }
  const decodePath = (path) => {
    try {
      return decodeURIComponent(path);
    } catch {
      return path;
    }
  };
  const cacheURL = (route) => new URL(mdfmtCache.key(new URL(route, siteRoot)));

  // A home-screen app launches at the manifest's start URL, the root page,
  // with a resume marker. Redirecting here, before the body is parsed, aborts
  // the directory page and lands on the last document read. A standalone app
  // with a one-entry history is also a launch, in case an auth redirect
  // dropped the marker. Regular browsers never resume: the root page without
  // the marker still opens as usual.
  if (rootPage && location.pathname === rootPage.pathname) {
    const params = new URLSearchParams(location.search);
    const launched = params.has("resume") || (navigator.standalone === true && history.length === 1);
    if (params.has("resume")) {
      // Drop the marker even when nothing is resumed so it never leaks into
      // later URLs.
      params.delete("resume");
      const query = params.toString();
      history.replaceState(null, "", location.pathname + (query ? `?${query}` : "") + location.hash);
    }
    // A root index.md is itself a document; never redirect to this page.
    const [latest] = launched ? loadRecent() : [];
    if (latest && latest.url !== identity) location.replace(recordURL(latest));
  }

  const init = () => {
    // The toggle buttons exist now; label them for the active mode.
    applyTheme(savedTheme());
    for (const button of document.querySelectorAll("[data-theme-toggle]")) {
      button.addEventListener("click", () => {
        const next = themeModes[(themeModes.indexOf(button.dataset.mode) + 1) % themeModes.length];
        applyTheme(next);
        try {
          localStorage.setItem(themeKey, next);
        } catch {
          // The choice still applies to this page when storage is unavailable.
        }
      });
    }

    applyNavCollapsed(root.classList.contains("nav-collapsed"));
    for (const button of document.querySelectorAll("[data-nav-toggle]")) {
      button.addEventListener("click", () => {
        const collapsed = !root.classList.contains("nav-collapsed");
        applyNavCollapsed(collapsed);
        navSetting.save(collapsed ? "collapsed" : "expanded");
      });
    }

    const relativeAge = (timestamp, compact) => {
      const then = new Date(timestamp);
      if (Number.isNaN(then.getTime())) return null;
      const elapsed = Math.max(0, Date.now() - then.getTime());
      const units = [
        [365 * 24 * 60 * 60 * 1000, "year", "y"],
        [30 * 24 * 60 * 60 * 1000, "month", "mo"],
        [7 * 24 * 60 * 60 * 1000, "week", "w"],
        [24 * 60 * 60 * 1000, "day", "d"],
        [60 * 60 * 1000, "hour", "h"],
        [60 * 1000, "minute", "m"],
      ];
      for (const [milliseconds, name, abbreviation] of units) {
        if (elapsed >= milliseconds) {
          const value = Math.floor(elapsed / milliseconds);
          return compact
            ? `${value}${abbreviation} ago`
            : `${value} ${name}${value === 1 ? "" : "s"} ago`;
        }
      }
      return "just now";
    };

    // The server renders the same strings, so the periodic refresh only
    // touches an element whose label has actually changed.
    const relativeTimes = Array.from(document.querySelectorAll("time[data-relative-time]"));
    const updateRelativeTimes = () => {
      for (const element of relativeTimes) {
        const value = relativeAge(
          element.dateTime,
          element.dataset.relativeStyle === "compact",
        );
        if (value && element.textContent !== value) element.textContent = value;
      }
    };
    updateRelativeTimes();
    if (relativeTimes.length) {
      setInterval(updateRelativeTimes, 60 * 1000);
    }

    // The toolbar popovers: one open at a time, closed by Escape, by a click
    // outside, or by choosing a link inside.
    const popovers = [];
    const closePopovers = () => {
      for (const popover of popovers) popover.set(false);
    };
    const registerPopover = (toggle, panel, apply) => {
      const set = (open) => {
        toggle.setAttribute("aria-expanded", String(open));
        apply(open);
      };
      toggle.addEventListener("click", () => {
        const open = toggle.getAttribute("aria-expanded") !== "true";
        closePopovers();
        if (open) set(true);
      });
      popovers.push({ toggle, panel, set });
    };
    document.addEventListener("click", (event) => {
      if (popovers.some((popover) => popover.toggle.contains(event.target))) return;
      const inside = popovers.some((popover) => popover.panel.contains(event.target));
      if (!inside || event.target.closest("a")) closePopovers();
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") closePopovers();
    });

    const tocToggle = document.querySelector("[data-toc-toggle]");
    const tocPanel = document.querySelector(".right-sidebar");
    if (tocToggle && tocPanel) {
      registerPopover(tocToggle, tocPanel, (open) => root.classList.toggle("toc-open", open));
    }

    const recent = loadRecent();
    const offlinePill = document.querySelector("[data-offline-pill]");
    if (offlinePill) {
      const setOffline = (offline) => {
        offlinePill.hidden = !offline;
      };
      setOffline(!navigator.onLine);
      addEventListener("online", () => setOffline(false));
      addEventListener("offline", () => setOffline(true));
      addEventListener("mdfmt:offline", () => setOffline(true));
    }

    // Mirrors humanSize in server.go.
    const formatSize = (size) => {
      if (size < 1024) return `${size} B`;
      let value = size / 1024;
      let exponent = 0;
      while (value >= 1024 && exponent < 5) {
        value /= 1024;
        exponent += 1;
      }
      return `${value.toFixed(1)} ${"KMGTPE"[exponent]}iB`;
    };
    const isPageRoute = (route) =>
      !route.startsWith(assetDir) &&
      (route === "" || route.endsWith("/") || route.endsWith(".html") || /\.(md|markdown)$/i.test(route));
    const cachedRoutes = async (cache) => {
      const routes = new Set();
      for (const request of await cache.keys()) {
        const url = cacheURL(request.url);
        if (url.search) continue;
        const route = siteRoute(url);
        if (route !== null) routes.add(route);
      }
      return routes;
    };
    const navLink = (href, title, detail) => {
      const link = document.createElement("a");
      link.className = "nav-entry";
      link.href = href;
      const name = document.createElement("span");
      name.className = "name";
      name.textContent = title;
      link.append(name);
      if (detail) {
        const span = document.createElement("span");
        span.className = "detail";
        span.textContent = detail;
        link.append(span);
      }
      return link;
    };

    // The offline page lists the cached documents, most recently read first.
    const offlineList = document.querySelector("[data-offline-list]");
    if (offlineList && assetDir && "caches" in window) {
      const from = new URLSearchParams(location.search).get("from");
      if (from) {
        document.querySelector("[data-offline-note]").textContent = `${decodePath(from)} is not available offline.`;
      }
      caches.open(cacheName).then(async (cache) => {
        const routes = Array.from(await cachedRoutes(cache)).filter(isPageRoute);
        const records = new Map(recent.map((record, index) => [siteRoute(cacheURL(record.url)), { index, title: record.title }]));
        const rank = (route) => records.get(route)?.index ?? recent.length;
        routes.sort((left, right) => rank(left) - rank(right) || left.localeCompare(right));
        const fragment = document.createDocumentFragment();
        for (const route of routes) {
          fragment.append(navLink(cacheURL(route).href, records.get(route)?.title || decodePath(route) || "Home"));
        }
        offlineList.replaceChildren(fragment);
      });
    }

    // The folder cache button stores every page below the directory in the
    // cache itself rather than relying on the service worker's fetch handler,
    // which does not see requests from an uncontrolled page (a shift-reload
    // or the first visit); the cached state is always derived from the cache.
    const cacheButton = document.querySelector("[data-cache-folder]");
    if (cacheButton && assetDir && "caches" in window) {
      const label = cacheButton.querySelector("[data-cache-label]");
      // Every page below this directory plus the images those pages reference.
      const folderTargets = (index) => {
        const prefix = siteRoute(cacheURL(new URL("./", location.href).href));
        const sizes = new Map(index.entries.map((entry) => [siteRoute(cacheURL(entry.url)), entry.size]));
        const targets = new Set();
        for (const entry of index.entries) {
          const route = siteRoute(cacheURL(entry.url));
          if (entry.kind !== "page" || !route.startsWith(prefix)) continue;
          targets.add(route);
          for (const image of entry.images || []) targets.add(siteRoute(cacheURL(image)));
        }
        const routes = Array.from(targets);
        return { routes, total: routes.reduce((sum, route) => sum + (sizes.get(route) || 0), 0) };
      };
      (async () => {
        const index = await fetch(new URL("site.json", new URL(assetDir, siteRoot)))
          .then((response) => (response.ok ? response.json() : null))
          .catch(() => null);
        if (!index) return; // without an index the button stays hidden
        const { routes, total } = folderTargets(index);
        const summary = `${routes.length} · ${formatSize(total)}`;
        const cache = await caches.open(cacheName);
        const refresh = async () => {
          const cached = await cachedRoutes(cache);
          const complete = routes.every((route) => cached.has(route));
          cacheButton.dataset.state = complete ? "cached" : "cache";
          label.textContent = complete ? `Offline · ${summary}` : `Cache ${summary}`;
          cacheButton.hidden = false;
          return cached;
        };
        const cacheFolder = async (cached) => {
          const queue = routes.filter((route) => !cached.has(route));
          let done = routes.length - queue.length;
          const worker = async () => {
            while (queue.length) {
              const url = cacheURL(queue.shift());
              try {
                const response = await fetch(url);
                if (mdfmtCache.cacheable(response)) await cache.put(url.href, response);
              } catch {
                // A failed fetch leaves the entry for the next attempt.
              }
              done += 1;
              label.textContent = `${done} / ${routes.length}`;
            }
          };
          await Promise.all([worker(), worker(), worker(), worker()]);
        };
        const removeFolder = async () => {
          for (const route of routes) await cache.delete(cacheURL(route).href);
        };
        let cached = await refresh();
        cacheButton.addEventListener("click", async () => {
          cacheButton.disabled = true;
          try {
            if (cacheButton.dataset.state === "cached") {
              if (confirm(`Remove ${routes.length} cached files from this device?`)) await removeFolder();
            } else {
              await cacheFolder(cached);
            }
          } finally {
            cacheButton.disabled = false;
            cached = await refresh();
          }
        });
      })();
    }

    // On narrow screens the toolbar is fixed to the bottom and its height
    // depends on how the breadcrumbs wrap; the stylesheet reads it to keep
    // the content and the popovers clear of it.
    const toolbar = document.querySelector(".page-toolbar");
    if (toolbar && "ResizeObserver" in window) {
      new ResizeObserver(() => {
        root.style.setProperty("--toolbar-height", `${toolbar.offsetHeight}px`);
      }).observe(toolbar);
    }

    const recentToggle = document.querySelector("[data-recent-toggle]");
    const recentMenu = document.querySelector("[data-recent-menu]");
    if (recentToggle && recentMenu) {
      const others = (list) => list.filter((record) => record.url !== identity);
      const showToggle = (list) => {
        recentToggle.hidden = others(list).length === 0;
      };
      const fill = () => {
        recentMenu.replaceChildren();
        for (const record of others(loadRecent())) {
          recentMenu.append(navLink(recordURL(record), record.title || record.url, relativeAge(record.updated, true)));
        }
        const clear = document.createElement("button");
        clear.type = "button";
        clear.className = "menu-clear";
        clear.textContent = "Clear";
        clear.addEventListener("click", () => {
          storeRecent([]);
          closePopovers();
          showToggle([]);
        });
        recentMenu.append(clear);
      };
      showToggle(recent);
      registerPopover(recentToggle, recentMenu, (open) => {
        if (open) fill();
        recentMenu.hidden = !open;
      });
    }

    // On narrow screens the full timestamp is hidden; a tap on the age
    // reveals it.
    document.querySelector(".page-meta time")?.addEventListener("click", (event) => {
      event.currentTarget.parentElement.classList.toggle("show-detail");
    });

    const topLink = document.querySelector("[data-top]");
    if (topLink) {
      topLink.addEventListener("click", (event) => {
        event.preventDefault();
        history.replaceState(null, "", location.pathname + location.search);
        scrollTo({ top: 0, behavior: "smooth" });
      });
    }

    const links = Array.from(document.querySelectorAll(".toc a[data-heading]"));
    const items = links
      .map((link) => ({ link, heading: document.getElementById(link.dataset.heading) }))
      .filter((item) => item.heading);

    // The heading at the reading line is the last one whose top is at or
    // above the toolbar. It drives both the TOC highlight and the saved
    // reading position.
    const scrollLine = 120;
    const currentItem = () => {
      let current = null;
      for (const item of items) {
        const top = item.heading.getBoundingClientRect().top;
        if (top > scrollLine) break;
        current = { item, top };
      }
      return current;
    };

    if (items.length) {
      let scheduled = false;
      const update = () => {
        scheduled = false;
        const current = currentItem()?.item || items[0];
        for (const item of items) {
          item.link.classList.toggle("current", item === current);
        }
      };
      const schedule = () => {
        if (!scheduled) {
          scheduled = true;
          requestAnimationFrame(update);
        }
      };

      addEventListener("scroll", schedule, { passive: true });
      addEventListener("resize", schedule);
      update();
    }

    if (document.querySelector("main.document-main")) {
      const title = document.title.replace(/ · mdfmt$/, "");
      // The position is the heading at the reading line plus the distance
      // scrolled past it, so it survives edits above the heading and a
      // changed viewport. Without a heading it is the raw scroll offset.
      const position = () => {
        const current = currentItem();
        return current
          ? { heading: current.item.heading.id, offset: Math.round(-current.top) }
          : { heading: "", offset: Math.round(scrollY) };
      };
      let saved = "";
      const savePosition = (list = loadRecent()) => {
        const { heading, offset } = position();
        const key = `${heading}\n${offset}`;
        if (key === saved) return;
        saved = key;
        const record = { url: identity, title, heading, offset, updated: Date.now() };
        storeRecent([record, ...list.filter((other) => other.url !== identity)]);
      };
      // Reload and back/forward leave scroll restoration to the browser; a
      // fragment means the reader followed a link to a specific heading.
      const restorePosition = () => {
        if (location.hash) return;
        const [navigation] = performance.getEntriesByType("navigation");
        if (navigation && navigation.type !== "navigate") return;
        const record = recent.find((other) => other.url === identity);
        if (!record) return;
        let top = record.offset;
        if (record.heading) {
          const heading = document.getElementById(record.heading);
          if (!heading) return;
          top += heading.getBoundingClientRect().top + scrollY;
        }
        scrollTo({ top: Math.max(0, top), behavior: "instant" });
      };
      restorePosition();
      savePosition(recent);

      // iOS may terminate a suspended app without running any final handler,
      // so the position is also saved shortly after every scroll.
      let saveTimer = 0;
      addEventListener(
        "scroll",
        () => {
          clearTimeout(saveTimer);
          saveTimer = setTimeout(savePosition, 250);
        },
        { passive: true },
      );
      document.addEventListener("visibilitychange", () => {
        if (document.visibilityState === "hidden") savePosition();
      });
      addEventListener("pagehide", savePosition);
    }

    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
    const compare = (left, right, key) => {
      if (key === "name") return collator.compare(left.dataset.name, right.dataset.name);
      return Number(left.dataset[key]) - Number(right.dataset[key]);
    };
    const sortKeysOf = (buttons) => new Set(buttons.map((button) => button.dataset.sortKey));

    // The sidebar file list: one fixed direction per key, most recent first
    // by default, remembered separately from the directory table.
    const fileList = document.querySelector("[data-file-list]");
    const sortControl = document.querySelector("[data-sidebar-sort]");
    if (fileList && sortControl) {
      const entries = Array.from(fileList.querySelectorAll("a[data-name]"));
      const sortButtons = Array.from(sortControl.querySelectorAll("button[data-sort-key]"));
      const descendingKeys = new Set(["modified", "size"]);
      const setting = persistedSetting("mdfmt.sidebar-sort", "mdfmt_sidebar_sort");
      const applyFileSort = (key, persist) => {
        const sign = descendingKeys.has(key) ? -1 : 1;
        const ordered = entries.slice().sort((left, right) => sign * compare(left, right, key));
        if (ordered.some((entry, index) => entry !== entries[index])) {
          const fragment = document.createDocumentFragment();
          fragment.append(...ordered);
          fileList.append(fragment);
          entries.splice(0, entries.length, ...ordered);
        }
        fileList.dataset.sort = key;
        for (const button of sortButtons) {
          button.setAttribute("aria-pressed", String(button.dataset.sortKey === key));
        }
        if (persist) setting.save(key);
      };
      const saved = setting.load();
      applyFileSort(sortKeysOf(sortButtons).has(saved) ? saved : "modified", false);
      for (const button of sortButtons) {
        button.addEventListener("click", () => applyFileSort(button.dataset.sortKey, true));
      }
    }

    const table = document.querySelector(".directory-table[data-sortable]");
    if (!table) return;

    const body = table.querySelector("tbody");
    const buttons = Array.from(table.querySelectorAll("button[data-sort-key]"));
    const validKeys = sortKeysOf(buttons);
    const setting = persistedSetting("mdfmt.directory-sort", "mdfmt_directory_sort");
    let activeKey = "name";
    let direction = "ascending";

    const applySort = (key, nextDirection, persist) => {
      activeKey = key;
      direction = nextDirection;

      const rows = Array.from(body.querySelectorAll("tr"));
      rows.sort((left, right) => {
        if (left.dataset.kind !== right.dataset.kind) {
          return left.dataset.kind === "directory" ? -1 : 1;
        }
        const result = compare(left, right, key);
        return direction === "ascending" ? result : -result;
      });
      for (const row of rows) body.append(row);

      for (const heading of table.querySelectorAll("th")) {
        heading.removeAttribute("aria-sort");
      }
      const activeButton = buttons.find((button) => button.dataset.sortKey === key);
      activeButton.closest("th").setAttribute("aria-sort", direction);

      if (persist) setting.save({ key, direction });
    };

    const saved = setting.load();
    if (
      saved &&
      validKeys.has(saved.key) &&
      (saved.direction === "ascending" || saved.direction === "descending")
    ) {
      activeKey = saved.key;
      direction = saved.direction;
    }
    applySort(activeKey, direction, false);

    for (const button of buttons) {
      button.addEventListener("click", () => {
        const key = button.dataset.sortKey;
        const nextDirection =
          activeKey === key && direction === "ascending" ? "descending" : "ascending";
        applySort(key, nextDirection, true);
      });
    }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
