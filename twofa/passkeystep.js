(function () {
  function supported() {
    return !!(window.PublicKeyCredential && PublicKeyCredential.parseRequestOptionsFromJSON);
  }
  function post(url, csrf, body) {
    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf, "Accept": "application/json", "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : "{}"
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) { throw new Error(j.error || "failed"); }
        return j;
      });
    });
  }
  document.querySelectorAll("[data-passkey-step]").forEach(function (btn) {
    var err = btn.parentNode.querySelector("[data-passkey-error]");
    if (!supported()) {
      btn.disabled = true;
      btn.title = btn.getAttribute("data-msg-unsupported") || "";
      return;
    }
    btn.addEventListener("click", function () {
      var csrf = btn.getAttribute("data-csrf") || "";
      var token = "";
      var finish = btn.getAttribute("data-finish");
      btn.disabled = true;
      if (err) { err.hidden = true; }
      post(btn.getAttribute("data-begin"), csrf).then(function (j) {
        token = j.token;
        return navigator.credentials.get({ publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(j.options.publicKey) });
      }).then(function (cred) {
        return post(finish + (finish.indexOf("?") === -1 ? "?" : "&") + "t=" + encodeURIComponent(token), csrf, cred.toJSON());
      }).then(function (j) {
        window.location.href = j.redirect;
      }).catch(function (e) {
        btn.disabled = false;
        console.warn("passkey step failed", e);
        if (err) {
          err.textContent = (e && e.name === "NotAllowedError" ? btn.getAttribute("data-msg-cancelled") : btn.getAttribute("data-msg-error")) || "";
          err.hidden = false;
        }
      });
    });
  });
})();
