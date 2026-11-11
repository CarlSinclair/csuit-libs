(function () {
  "use strict";

  var ENDPOINT = "/api/client-error";
  var MAX_PER_PAGE = 20;
  var sent = 0;
  var seen = {};

  function pageLocation() {
    return window.location.host + window.location.pathname;
  }

  function report(context, err, extra) {
    try {
      var message = err && err.message ? String(err.message) : (err ? String(err) : "");
      var dedupe = String(context || "") + "|" + message.slice(0, 200) + "|" + (extra && extra.message ? String(extra.message).slice(0, 200) : "");
      if (seen[dedupe] || sent >= MAX_PER_PAGE) return;
      seen[dedupe] = true;
      sent += 1;
      var payload = {
        context: String(context || ""),
        message: message,
        stack: err && err.stack ? String(err.stack).slice(0, 2000) : "",
        extra: extra || null,
        url: pageLocation(),
        user_agent: navigator.userAgent
      };
      window.fetch(ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
        keepalive: true
      }).catch(function () {});
    } catch (_) {}
  }

  window.reportClientError = window.reportClientError || report;

  if (window.console && console.error && !console.error.__clientErrorWrapped) {
    var native = console.error.bind(console);
    var wrapped = function () {
      native.apply(console, arguments);
      try {
        var parts = Array.prototype.slice.call(arguments).map(function (a) {
          if (a instanceof Error) return a.message;
          if (typeof a === "object") { try { return JSON.stringify(a); } catch (_) { return String(a); } }
          return String(a);
        });
        report("console.error", null, { message: parts.join(" ").slice(0, 2000) });
      } catch (_) {}
    };
    wrapped.__clientErrorWrapped = true;
    console.error = wrapped;
  }

  window.addEventListener("error", function (ev) {
    report("window.error", ev.error || ev.message, { file: ev.filename, line: ev.lineno });
  });

  window.addEventListener("unhandledrejection", function (ev) {
    report("unhandledrejection", ev.reason);
  });
})();
