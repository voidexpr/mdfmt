(() => {
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
})();
