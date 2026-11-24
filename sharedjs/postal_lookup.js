(function () {
  if (window.__postalLookupBound) return;
  window.__postalLookupBound = true;

  var tokens = new WeakMap();

  function countryOf(input) {
    var from = input.getAttribute("data-postal-country-from");
    if (from) {
      var el = document.querySelector(from);
      return el ? String(el.value || "").trim().toUpperCase() : "";
    }
    return String(input.getAttribute("data-postal-country") || "").trim().toUpperCase();
  }

  function show(input, text, state) {
    var box = document.querySelector(input.getAttribute("data-postal-result") || "");
    if (!box) return;
    box.textContent = text || "";
    box.hidden = !text;
    if (state) box.setAttribute("data-postal-state", state);
    else box.removeAttribute("data-postal-state");
  }

  function fillStreet(input, place) {
    var target = document.querySelector(input.getAttribute("data-postal-fill-street") || "");
    if (!target || !place.street || String(target.value || "").trim() !== "") return;
    target.value = place.district ? place.street + ", " + place.district : place.street;
    target.dispatchEvent(new Event("input", { bubbles: true }));
  }

  function lookup(input) {
    var code = String(input.value || "").trim();
    var country = countryOf(input);
    var url = input.getAttribute("data-postal-lookup");
    var token = (tokens.get(input) || 0) + 1;
    tokens.set(input, token);
    if (!code || !country || !url) {
      show(input, "", "");
      return;
    }
    fetch(url + "?country=" + encodeURIComponent(country) + "&code=" + encodeURIComponent(code), {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    })
      .then(function (res) {
        return res.json().then(
          function (data) { return { status: res.status, data: data }; },
          function () { return { status: res.status, data: null }; }
        );
      })
      .then(function (r) {
        if (tokens.get(input) !== token) return;
        var places = r.data && r.data.places;
        if (r.status >= 200 && r.status < 300 && places && places.length) {
          var p = places[0];
          show(input, [p.city, p.region_code || p.region].filter(Boolean).join(", "), "ok");
          fillStreet(input, p);
          return;
        }
        if (r.status === 422) {
          show(input, "", "");
          return;
        }
        if (r.status === 400 || r.status === 404) {
          show(input, input.getAttribute("data-postal-notfound") || "", "error");
          return;
        }
        console.error("postal lookup failed", { status: r.status, country: country, code: code });
        show(input, input.getAttribute("data-postal-failed") || "", "error");
      })
      .catch(function (err) {
        if (tokens.get(input) !== token) return;
        console.error("postal lookup network error", err);
        show(input, input.getAttribute("data-postal-failed") || "", "error");
      });
  }

  document.addEventListener("change", function (e) {
    var el = e.target && e.target.closest && e.target.closest("[data-postal-lookup]");
    if (el) lookup(el);
  });
})();
