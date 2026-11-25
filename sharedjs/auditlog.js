(function () {
  "use strict";
  if (window.csAuditLog) return;
  window.csAuditLog = { version: 1 };
  var submit = function (e) {
    var t = e.target;
    if (!t || !t.form || !t.form.hasAttribute("data-auditlog-filter")) return;
    if (t.matches("input[type=text],input[type=search]")) return;
    t.form.requestSubmit ? t.form.requestSubmit() : t.form.submit();
  };
  document.addEventListener("change", submit);
  document.addEventListener("keydown", function (e) {
    var t = e.target;
    if (e.key === "Enter" && t && t.form && t.form.hasAttribute("data-auditlog-filter")) {
      e.preventDefault();
      t.form.requestSubmit ? t.form.requestSubmit() : t.form.submit();
    }
  });
})();
