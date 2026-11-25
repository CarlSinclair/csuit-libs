(function () {
  "use strict";
  function init() {
  var root = document.querySelector(".legal-pages-editor");
  if (!root || root.getAttribute("data-lp-bound")) return;
  root.setAttribute("data-lp-bound", "1");
  var checkURL = root.getAttribute("data-check-url") || "";
  var invalidLabel = root.getAttribute("data-slug-invalid-label") || "";
  var availableLabel = root.getAttribute("data-slug-available-label") || "";
  var takenLabel = root.getAttribute("data-slug-taken-label") || "";
  var failedLabel = root.getAttribute("data-slug-failed-label") || "";

  function show(status, text, ok) {
    status.textContent = text;
    status.className = "slug-status" + (text ? (ok ? " is-ok" : " is-bad") : "");
  }

  function slugValid(v) {
    return v === "" || /^[a-z0-9][a-z0-9-]*$/.test(v);
  }

  var timers = new WeakMap();
  root.addEventListener("input", function (e) {
    var input = e.target.closest("[data-slug-input]");
    if (!input) return;
    var status = input.parentElement.querySelector(".slug-status");
    if (!status) return;
    var v = input.value.trim().toLowerCase();
    if (input.value !== v) input.value = v;

    if (!slugValid(v)) {
      show(status, invalidLabel, false);
      return;
    }
    if (v === "" || !checkURL) {
      show(status, "", true);
      return;
    }
    clearTimeout(timers.get(input));
    timers.set(
      input,
      setTimeout(function () {
        var pid = input.getAttribute("data-page-id") || "0";
        fetch(checkURL + "?slug=" + encodeURIComponent(v) + "&page_id=" + encodeURIComponent(pid), {
          headers: { Accept: "application/json" },
        })
          .then(function (r) {
            if (!r.ok) throw new Error("status " + r.status);
            return r.json();
          })
          .then(function (j) {
            if (input.value.trim().toLowerCase() !== v) return;
            if (j && j.available) show(status, availableLabel, true);
            else show(status, takenLabel, false);
          })
          .catch(function () { show(status, failedLabel, false); });
      }, 300)
    );
  });

  var addForm = root.querySelector(".lp-add-form");
  if (addForm) {
    var langSel = addForm.querySelector("[data-same-lang]");
    var sameSel = addForm.querySelector("[data-same-select]");
    var sameToggle = addForm.querySelector("[data-same-toggle]");
    var sameGroup = addForm.querySelector("[data-same-group]");
    var resolvedEl = addForm.querySelector("[data-lang-resolved]");
    var resolveUrl = langSel ? langSel.getAttribute("data-resolve-url") : "";
    var resolvedCode = "";
    var resolveTimer = null;
    var syncSame = function () {
      if (!sameSel) return;
      var lang = resolvedCode.toLowerCase();
      var qualifying = 0;
      Array.prototype.forEach.call(sameSel.options, function (o) {
        if (o.value === "") return;
        var taken = lang !== "" && (o.getAttribute("data-langs") || "").toLowerCase().split(" ").indexOf(lang) !== -1;
        o.hidden = taken;
        o.disabled = taken;
        if (!taken) qualifying++;
      });
      if (sameGroup) sameGroup.classList.toggle("hidden", qualifying === 0);
      if (qualifying === 0 && sameToggle) sameToggle.checked = false;
      if (sameSel.selectedOptions.length && sameSel.selectedOptions[0].disabled) sameSel.value = "";
      if (sameToggle) sameSel.required = sameToggle.checked;
    };
    var resolveLang = function () {
      var q = langSel.value.trim();
      if (!q || !resolveUrl) {
        resolvedCode = "";
        if (resolvedEl) resolvedEl.textContent = "";
        syncSame();
        return;
      }
      fetch(resolveUrl + "?q=" + encodeURIComponent(q), { credentials: "same-origin", headers: { "X-Requested-With": "XMLHttpRequest" } })
        .then(function (resp) {
          if (!resp.ok) throw new Error("status " + resp.status);
          return resp.json();
        })
        .then(function (data) {
          if (langSel.value.trim() !== q) return;
          resolvedCode = data.code || "";
          if (resolvedEl) resolvedEl.textContent = data.label || "";
          syncSame();
        })
        .catch(function (err) {
          console.error("legalpages: language resolve failed", { q: q, err: err });
        });
    };
    if (langSel) {
      langSel.addEventListener("input", function () {
        window.clearTimeout(resolveTimer);
        resolveTimer = window.setTimeout(resolveLang, 250);
      });
    }
    if (sameToggle) sameToggle.addEventListener("change", syncSame);
    syncSame();
  }

  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
