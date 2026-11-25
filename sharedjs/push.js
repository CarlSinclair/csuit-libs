(function () {
  "use strict";
  if (window.csPush) return;
  window.csPush = { version: 1 };

  function b64(s) {
    var pad = "=".repeat((4 - (s.length % 4)) % 4);
    var raw = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
    var out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }

  function post(url, body) {
    return fetch(url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); });
  }

  function supported() {
    return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
  }

  function setState(btn, state) {
    btn.setAttribute("data-push-state", state);
    var label = btn.getAttribute("data-label-" + state);
    if (label) btn.textContent = label;
    btn.disabled = state === "unsupported" || state === "denied";
  }

  function init(btn) {
    var prefix = btn.getAttribute("data-push-prefix") || "/push";
    if (!supported()) { setState(btn, "unsupported"); return; }
    if (Notification.permission === "denied") { setState(btn, "denied"); return; }
    navigator.serviceWorker.register("/sw.js").then(function (reg) {
      return reg.pushManager.getSubscription().then(function (sub) {
        setState(btn, sub ? "on" : "off");
        btn.addEventListener("click", function () {
          btn.disabled = true;
          var done = function (state) { setState(btn, state); btn.disabled = false; };
          var fail = function (err) { console.error("push:", err); setState(btn, "error"); btn.disabled = false; };
          reg.pushManager.getSubscription().then(function (current) {
            if (current) {
              var ep = current.endpoint;
              return current.unsubscribe().then(function () { return post(prefix + "/unsubscribe", { endpoint: ep }); }).then(function () { done("off"); });
            }
            return Notification.requestPermission().then(function (perm) {
              if (perm !== "granted") { done(perm === "denied" ? "denied" : "off"); return; }
              return fetch(prefix + "/key", { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (k) {
                return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(k.key) });
              }).then(function (sub) {
                var j = sub.toJSON();
                return post(prefix + "/subscribe", { endpoint: j.endpoint, keys: j.keys });
              }).then(function () { done("on"); });
            });
          }).catch(fail);
        });
      });
    }).catch(function (err) { console.error("push: service worker", err); setState(btn, "error"); });
  }

  function boot() {
    Array.prototype.forEach.call(document.querySelectorAll("[data-push-toggle]"), init);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot); else boot();
})();
