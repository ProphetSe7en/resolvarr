// theme-init.js — runs synchronously in <head> before CSS paints to
// resolve the user's saved theme. Avoids the flash-of-wrong-theme
// that hits if Alpine waits to apply the data-theme attribute.
//
// Loaded as a separate file (not inline) so the CSP can stay strict
// with no 'unsafe-inline' on script-src. Mirrors the applyTheme()
// logic in app.js for the first paint; Alpine takes over after.
//
// try/catch — Safari Private Mode and sandboxed iframes throw on
// localStorage access; missing matchMedia in old browsers also
// crashes. Fall back to 'dark' so the UI is at least styled.
(function () {
  try {
    var stored =
      (typeof localStorage !== "undefined" &&
        localStorage.getItem("resolvarr-theme")) ||
      "system";
    var mql =
      typeof matchMedia === "function"
        ? matchMedia("(prefers-color-scheme: light)")
        : null;
    var resolved =
      stored === "system" ? (mql && mql.matches ? "light" : "dark") : stored;
    document.documentElement.setAttribute("data-theme", resolved);
  } catch (e) {
    document.documentElement.setAttribute("data-theme", "dark");
  }
})();
