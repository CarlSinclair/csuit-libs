(function () {
  if (window.csLocalTime) return;
  window.csLocalTime = { version: 1 };
  var absolute = null;
  var relative = null;
  function formats() {
    if (absolute) return;
    var lang = navigator.language || undefined;
    try {
      absolute = new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" });
      relative = new Intl.RelativeTimeFormat(lang, { numeric: "auto" });
    } catch (e) {
      absolute = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
      relative = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
    }
  }
  function human(date) {
    var seconds = Math.round((date.getTime() - Date.now()) / 1000);
    var abs = Math.abs(seconds);
    if (abs < 60) return relative.format(seconds, "second");
    if (abs < 3600) return relative.format(Math.round(seconds / 60), "minute");
    if (abs < 86400) return relative.format(Math.round(seconds / 3600), "hour");
    return absolute.format(date);
  }
  function apply(root) {
    formats();
    var scope = root && root.querySelectorAll ? root : document;
    scope.querySelectorAll("time[data-local-time]:not([data-local-done])").forEach(function (el) {
      var date = new Date(el.getAttribute("datetime"));
      if (isNaN(date.getTime())) return;
      el.textContent = human(date);
      el.title = absolute.format(date);
      el.setAttribute("data-local-done", "1");
    });
  }
  function start() {
    apply(document);
    new MutationObserver(function (list) {
      list.forEach(function (m) {
        m.addedNodes.forEach(function (n) { if (n.nodeType === 1) apply(n); });
      });
    }).observe(document.body, { childList: true, subtree: true });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
