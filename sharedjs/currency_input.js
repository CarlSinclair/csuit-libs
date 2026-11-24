(function () {
  function isCurrencyInput(el) {
    return el && el.matches && el.matches("input[data-currency-input]");
  }
  function normalize(el) {
    el.value = el.value.trim().toUpperCase();
  }
  function known(el, value) {
    if (value === "" && el.dataset.allowEmpty === "1") return true;
    var list = document.getElementById(el.getAttribute("list"));
    if (!list) return false;
    return Array.prototype.some.call(list.options, function (o) {
      return o.value.toUpperCase() === value;
    });
  }
  function normalizeAll() {
    Array.prototype.forEach.call(document.querySelectorAll("input[data-currency-input]"), normalize);
  }
  document.addEventListener("focusin", function (e) {
    if (!isCurrencyInput(e.target)) return;
    normalize(e.target);
    e.target.dataset.lastValid = e.target.value;
  });
  document.addEventListener("change", function (e) {
    var el = e.target;
    if (!isCurrencyInput(el)) return;
    var typed = el.value.trim().toUpperCase();
    if (!known(el, typed)) {
      el.value = el.dataset.lastValid || "";
      e.stopImmediatePropagation();
      return;
    }
    el.value = typed;
    el.dataset.lastValid = typed;
  }, true);
  window.initCurrencyInputs = normalizeAll;
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", normalizeAll);
  } else {
    normalizeAll();
  }
})();
