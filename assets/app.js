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

    if (items.length) {
      let scheduled = false;
      const update = () => {
        scheduled = false;
        let current = items[0];
        for (const item of items) {
          if (item.heading.getBoundingClientRect().top <= 120) current = item;
          else break;
        }
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
