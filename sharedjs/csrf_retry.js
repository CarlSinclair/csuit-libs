(function () {
  if (!window.fetch || window.__csrfRetryInstalled) return;
  window.__csrfRetryInstalled = true;
  var nativeFetch = window.fetch.bind(window);

  function metaEl() {
    return document.querySelector('meta[name="csrf-token"]');
  }

  function refreshToken() {
    return nativeFetch(window.location.pathname + window.location.search, {
      credentials: "same-origin",
      headers: { "Accept": "text/html" }
    }).then(function (resp) {
      return resp.text();
    }).then(function (html) {
      var m = html.match(/<meta name="csrf-token" content="([^"]*)"/);
      var meta = metaEl();
      if (!m || !m[1] || !meta) return "";
      meta.setAttribute("content", m[1]);
      return m[1];
    }).catch(function () {
      return "";
    });
  }

  function resendable(body) {
    return body === undefined || body === null || typeof body === "string" ||
      body instanceof FormData || body instanceof URLSearchParams;
  }

  window.fetch = function (input, init) {
    return nativeFetch(input, init).then(function (resp) {
      if (resp.status !== 403 || typeof input !== "string" && !(input instanceof URL)) return resp;
      var opts = init || {};
      var method = String(opts.method || "GET").toUpperCase();
      if (method === "GET" || method === "HEAD" || !resendable(opts.body)) return resp;
      var headers = new Headers(opts.headers || {});
      var sent = headers.get("X-CSRF-Token");
      if (!sent) return resp;
      var target = new URL(input, window.location.href);
      if (target.origin !== window.location.origin) return resp;
      return refreshToken().then(function (fresh) {
        if (!fresh || fresh === sent) return resp;
        headers.set("X-CSRF-Token", fresh);
        var retry = {};
        Object.keys(opts).forEach(function (k) { retry[k] = opts[k]; });
        retry.headers = headers;
        return nativeFetch(input, retry);
      });
    });
  };
})();
