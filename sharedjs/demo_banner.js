(function () {
  var el = document.querySelector(".demo-banner[data-demo-seconds]");
  if (!el) return;
  var template = el.getAttribute("data-demo-template") || "%s";
  var end = Date.now() + (parseInt(el.getAttribute("data-demo-seconds"), 10) || 0) * 1000;
  function pad(n) {
    return n < 10 ? "0" + n : String(n);
  }
  function render() {
    var left = Math.max(0, Math.round((end - Date.now()) / 1000));
    var text = pad(Math.floor(left / 3600)) + ":" + pad(Math.floor((left % 3600) / 60)) + ":" + pad(left % 60);
    el.textContent = template.replace("%s", text);
  }
  render();
  setInterval(render, 1000);
})();
