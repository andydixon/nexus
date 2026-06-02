(function () {
  /**
   * Nexus Terminal — utility functions.
   * General-purpose helpers used throughout the application.
   */

  /**
   * Shorthand for document.querySelector.
   * @param {string} selector - CSS selector.
   * @returns {Element|null}
   */
  function $(selector) {
    return document.querySelector(selector);
  }

  /**
   * Shorthand for document.querySelectorAll.
   * @param {string} selector - CSS selector.
   * @returns {NodeListOf<Element>}
   */
  function $$(selector) {
    return document.querySelectorAll(selector);
  }

  /**
   * Creates a DOM element with optional class name and attributes.
   * @param {string} tag - HTML tag name.
   * @param {string} [className] - Space-separated class names.
   * @param {Object} [attrs] - Attribute key/value pairs.
   * @returns {HTMLElement}
   */
  function createElement(tag, className, attrs = {}) {
    const el = document.createElement(tag);
    if (className) {
      el.className = className;
    }
    for (const [key, value] of Object.entries(attrs)) {
      if (key === "textContent") {
        el.textContent = value;
      } else if (key === "innerHTML") {
        el.innerHTML = value;
      } else if (key === "dataset") {
        for (const [dKey, dVal] of Object.entries(value)) {
          el.dataset[dKey] = dVal;
        }
      } else if (key.startsWith("on") && typeof value === "function") {
        el.addEventListener(key.slice(2).toLowerCase(), value);
      } else {
        el.setAttribute(key, value);
      }
    }
    return el;
  }

  /**
   * Escapes HTML special characters to prevent XSS.
   * @param {string} str - Raw string.
   * @returns {string} Escaped string safe for innerHTML.
   */
  function escapeHtml(str) {
    if (!str) return "";
    const map = {
      "&": "&amp;",
      "<": "&lt;",
      ">": "&gt;",
      '"': "&quot;",
      "'": "&#039;",
    };
    return str.replace(/[&<>"']/g, (ch) => map[ch]);
  }

  /**
   * Formats a Date (or ISO string) into a concise timestamp.
   * @param {Date|string} date
   * @returns {string} e.g. "14:32:07" or "2 Jan 14:32".
   */
  function formatTimestamp(date) {
    if (!date) return "";
    const d = date instanceof Date ? date : new Date(date);
    if (isNaN(d.getTime())) return "";

    const now = new Date();
    const isToday = d.toDateString() === now.toDateString();
    const hours = String(d.getHours()).padStart(2, "0");
    const mins = String(d.getMinutes()).padStart(2, "0");
    const secs = String(d.getSeconds()).padStart(2, "0");

    if (isToday) {
      return `${hours}:${mins}:${secs}`;
    }
    const months = [
      "Jan",
      "Feb",
      "Mar",
      "Apr",
      "May",
      "Jun",
      "Jul",
      "Aug",
      "Sep",
      "Oct",
      "Nov",
      "Dec",
    ];
    return `${d.getDate()} ${months[d.getMonth()]} ${hours}:${mins}`;
  }

  /**
   * Returns a debounced version of the given function.
   * @param {Function} fn
   * @param {number} ms - Delay in milliseconds.
   * @returns {Function}
   */
  function debounce(fn, ms) {
    let timer = null;
    return function (...args) {
      clearTimeout(timer);
      timer = setTimeout(() => fn.apply(this, args), ms);
    };
  }

  /**
   * Performs a simple fuzzy match — checks whether every character in
   * the query appears in order within the text (case-insensitive).
   * @param {string} text
   * @param {string} query
   * @returns {{ match: boolean, score: number }}
   */
  function fuzzyMatch(text, query) {
    if (!query) return { match: true, score: 1 };
    const lowerText = text.toLowerCase();
    const lowerQuery = query.toLowerCase();

    let textIdx = 0;
    let score = 0;
    let consecutiveBonus = 0;

    for (let qi = 0; qi < lowerQuery.length; qi++) {
      const ch = lowerQuery[qi];
      const foundAt = lowerText.indexOf(ch, textIdx);
      if (foundAt === -1) {
        return { match: false, score: 0 };
      }
      // Reward consecutive character matches and early matches
      if (foundAt === textIdx) {
        consecutiveBonus += 1;
      } else {
        consecutiveBonus = 0;
      }
      score += 1 + consecutiveBonus + (foundAt === 0 ? 2 : 0);
      textIdx = foundAt + 1;
    }

    return { match: true, score };
  }

  /**
   * Base64-encodes a string.
   * @param {string} str
   * @returns {string}
   */
  function base64Encode(str) {
    return btoa(str);
  }

  /**
   * UTF-8-encodes a string and then base64-encodes the bytes.
   * Unlike bare btoa(), this handles characters above U+007F correctly.
   * @param {string} str
   * @returns {string}
   */
  function utf8ToBase64(str) {
    const bytes = new TextEncoder().encode(str);
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }

  /**
   * Base64-decodes a string.
   * @param {string} str
   * @returns {string}
   */
  function base64Decode(str) {
    return atob(str);
  }

  /**
   * Generates a new UUID v4 using the browser crypto API.
   * @returns {string}
   */
  function generateId() {
    return crypto.randomUUID();
  }

  /**
   * Formats a byte count into a human-friendly string.
   * @param {number} bytes
   * @returns {string} e.g. "1.23 KB"
   */
  function formatBytes(bytes) {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const k = 1024;
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    const value = bytes / Math.pow(k, i);
    return `${value.toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
  }

  /**
   * Truncates a string to the given length, appending an ellipsis if needed.
   * @param {string} str
   * @param {number} len
   * @returns {string}
   */
  function truncate(str, len) {
    if (!str || str.length <= len) return str || "";
    return str.slice(0, len) + "…";
  }

  /**
   * Removes any open context menus from the DOM.
   */
  function dismissContextMenus() {
    document.querySelectorAll(".context-menu").forEach((m) => m.remove());
  }

  /**
   * Returns the protocol badge colour class.
   * @param {string} protocol - 'ssh', 'telnet', 'raw', or 'github'.
   * @returns {string}
   */
  function protocolColour(protocol) {
    switch (protocol) {
      case "ssh":
        return "proto-ssh";
      case "telnet":
        return "proto-telnet";
      case "raw":
        return "proto-raw";
      case "github":
        return "proto-github";
      case "vnc":
        return "proto-vnc";
      case "local":
        return "proto-local";
      default:
        return "";
    }
  }

  /**
   * Returns a human-friendly relative time string for an ISO date.
   * @param {string} isoDate - ISO 8601 date string.
   * @returns {string} e.g. "3 hours ago", "2 days ago".
   */
  function relativeTime(isoDate) {
    if (!isoDate) return "";
    const d = new Date(isoDate);
    if (isNaN(d.getTime())) return "";

    const now = Date.now();
    const diff = now - d.getTime();
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return "just now";
    const mins = Math.floor(secs / 60);
    if (mins < 60) return mins === 1 ? "1 minute ago" : `${mins} minutes ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs === 1 ? "1 hour ago" : `${hrs} hours ago`;
    const days = Math.floor(hrs / 24);
    if (days < 30) return days === 1 ? "1 day ago" : `${days} days ago`;
    const months = Math.floor(days / 30);
    if (months < 12)
      return months === 1 ? "1 month ago" : `${months} months ago`;
    const years = Math.floor(months / 12);
    return years === 1 ? "1 year ago" : `${years} years ago`;
  }

  /**
   * Returns a human-friendly label for a session state.
   * @param {string} state
   * @returns {string}
   */
  function stateLabel(state) {
    switch (state) {
      case "connecting":
        return "Connecting";
      case "connected":
        return "Connected";
      case "disconnected":
        return "Disconnected";
      case "reconnecting":
        return "Reconnecting";
      case "error":
        return "Error";
      default:
        return state || "Unknown";
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  Object.assign(window.NexusTerminal, {
    $,
    $$,
    createElement,
    escapeHtml,
    formatTimestamp,
    debounce,
    fuzzyMatch,
    base64Encode,
    utf8ToBase64,
    base64Decode,
    generateId,
    formatBytes,
    truncate,
    dismissContextMenus,
    protocolColour,
    stateLabel,
    relativeTime,
  });

  /* ── Disable macOS/WebKit auto-capitalisation on all text inputs ───────── */
  function disableAutoCapitalise(el) {
    el.setAttribute("autocapitalize", "none");
    el.setAttribute("autocorrect", "off");
    el.setAttribute("spellcheck", "false");
  }

  // Apply to existing inputs
  document.querySelectorAll("input, textarea").forEach(disableAutoCapitalise);

  // Observe for dynamically added inputs
  var inputObserver = new MutationObserver(function (mutations) {
    for (var i = 0; i < mutations.length; i++) {
      var nodes = mutations[i].addedNodes;
      for (var j = 0; j < nodes.length; j++) {
        var node = nodes[j];
        if (node.nodeType !== 1) continue;
        if (node.tagName === "INPUT" || node.tagName === "TEXTAREA") {
          disableAutoCapitalise(node);
        }
        if (node.querySelectorAll) {
          node
            .querySelectorAll("input, textarea")
            .forEach(disableAutoCapitalise);
        }
      }
    }
  });
  inputObserver.observe(document.body, { childList: true, subtree: true });
})();
