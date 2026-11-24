(function () {
  "use strict";

  function meta(name) {
    var el = document.querySelector('meta[name="' + name + '"]');
    return el ? el.getAttribute("content") || "" : "";
  }

  function formatBytes(n) {
    if (n >= 1073741824) return (n / 1073741824).toFixed(1).replace(/\.0$/, "") + " GB";
    if (n >= 1048576) return (n / 1048576).toFixed(1).replace(/\.0$/, "") + " MB";
    if (n >= 1024) return Math.round(n / 1024) + " KB";
    return n + " B";
  }

  function format(template, size, max) {
    return String(template).replace("{size}", formatBytes(size)).replace("{max}", formatBytes(max));
  }

  var defaultMax = parseInt(meta("upload-max-bytes"), 10) || 0;
  var totalMax = parseInt(meta("upload-total-bytes"), 10) || 0;
  var fileTemplate = meta("upload-error") || "";
  var totalTemplate = meta("upload-total-error") || "";

  function limitFor(input) {
    var own = parseInt(input.getAttribute("data-max-bytes"), 10);
    return own > 0 ? own : defaultMax;
  }

  function errorBox(input) {
    var host = input.closest(".form-group") || input.parentNode;
    var box = host.querySelector(".upload-error");
    if (!box) {
      box = document.createElement("div");
      box.className = "upload-error";
      box.setAttribute("role", "alert");
      box.hidden = true;
      host.appendChild(box);
    }
    return box;
  }

  function showError(input, text) {
    var box = errorBox(input);
    box.textContent = text;
    box.hidden = false;
    input.setAttribute("aria-invalid", "true");
  }

  function clearError(input) {
    var host = input.closest(".form-group") || input.parentNode;
    var box = host.querySelector(".upload-error");
    if (box) box.hidden = true;
    input.removeAttribute("aria-invalid");
  }

  function check(input) {
    clearError(input);
    var max = limitFor(input);
    if (!max || !input.files) return true;
    for (var i = 0; i < input.files.length; i++) {
      if (input.files[i].size > max) {
        showError(input, format(fileTemplate, input.files[i].size, max));
        input.value = "";
        return false;
      }
    }
    return true;
  }

  document.addEventListener("change", function (e) {
    var t = e.target;
    if (t && t.tagName === "INPUT" && t.type === "file") check(t);
  });

  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.querySelectorAll) return;
    var inputs = Array.prototype.slice.call(form.querySelectorAll('input[type="file"]'));
    var total = 0;
    var first = null;
    for (var i = 0; i < inputs.length; i++) {
      var files = inputs[i].files;
      if (!files || !files.length) continue;
      if (!first) first = inputs[i];
      for (var j = 0; j < files.length; j++) {
        var max = limitFor(inputs[i]);
        if (max && files[j].size > max) {
          e.preventDefault();
          e.stopImmediatePropagation();
          showError(inputs[i], format(fileTemplate, files[j].size, max));
          return;
        }
        total += files[j].size;
      }
    }
    if (totalMax && total > totalMax && first) {
      e.preventDefault();
      e.stopImmediatePropagation();
      showError(first, format(totalTemplate, total, totalMax));
    }
  }, true);
})();
