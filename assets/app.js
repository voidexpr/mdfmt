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

  // Reading positions live in one list of the most recently viewed documents.
  // The page announces its root page (the directory page in serve, index.html
  // in a build). A document is identified by its path relative to the root
  // directory, so records survive a rotated path token or a moved site.
  const recentKey = "mdfmt.recent";
  const recentLimit = 20;
  const rootPage = root.dataset.root === undefined ? null : new URL(root.dataset.root, location.href);
  const siteRoot = rootPage && new URL("./", rootPage);
  const identity =
    siteRoot && location.pathname.startsWith(siteRoot.pathname)
      ? location.pathname.slice(siteRoot.pathname.length)
      : location.pathname;

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

    const updateRelativeTimes = () => {
      for (const element of document.querySelectorAll("time[data-relative-time]")) {
        const value = relativeAge(
          element.dateTime,
          element.dataset.relativeStyle === "compact",
        );
        if (value) element.textContent = value;
      }
    };
    updateRelativeTimes();
    if (document.querySelector("time[data-relative-time]")) {
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
          const link = document.createElement("a");
          link.className = "nav-entry";
          link.href = recordURL(record);
          const title = document.createElement("span");
          title.className = "name";
          title.textContent = record.title || record.url;
          const age = document.createElement("span");
          age.className = "size";
          age.textContent = relativeAge(record.updated, true);
          link.append(title, age);
          recentMenu.append(link);
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

    const table = document.querySelector(".directory-table[data-sortable]");
    if (!table) return;

    const body = table.querySelector("tbody");
    const buttons = Array.from(table.querySelectorAll("button[data-sort-key]"));
    const storageKey = "mdfmt.directory-sort";
    const cookieKey = "mdfmt_directory_sort";
    const validKeys = new Set(["name", "modified", "size"]);
    let activeKey = "name";
    let direction = "ascending";

    const compare = (left, right, key) => {
      if (key === "name") {
        return left.dataset.name.localeCompare(right.dataset.name, undefined, {
          numeric: true,
          sensitivity: "base",
        });
      }
      return Number(left.dataset[key]) - Number(right.dataset[key]);
    };

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

      if (persist) {
        const serialized = JSON.stringify({ key, direction });
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
          // Sorting still works when browser storage is disabled.
        }
      }
    };

    let saved = null;
    try {
      saved = JSON.parse(localStorage.getItem(storageKey));
    } catch {
      // Try the cookie below.
    }
    if (!saved) {
      try {
        const prefix = `${cookieKey}=`;
        const cookie = document.cookie
          .split(";")
          .map((part) => part.trim())
          .find((part) => part.startsWith(prefix));
        if (cookie) {
          saved = JSON.parse(decodeURIComponent(cookie.slice(prefix.length)));
          localStorage.setItem(storageKey, JSON.stringify(saved));
        }
      } catch {
        // Use the filename-ascending default when storage is unavailable or invalid.
      }
    }
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
