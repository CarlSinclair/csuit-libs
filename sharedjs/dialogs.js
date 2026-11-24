(function () {
  "use strict";

  var CFG = { overlay: "modal", open: "js-open", box: "modal-content", titleTag: "h2" };
  var self = document.currentScript;
  if (self && self.getAttribute("data-dialog") === "bots") {
    CFG = { overlay: "modal-overlay", open: "active", box: "modal", titleTag: "span" };
  }
  var overlay = null;
  var els = {};
  var current = null;
  var lastFocus = null;

  function data(name) {
    return (document.body && document.body.dataset[name]) || "";
  }

  function make(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }

  function build() {
    if (overlay) return;
    overlay = make("div", CFG.overlay);
    overlay.id = "app-dialog";
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    var box = make("div", CFG.box);
    var head = make("div", "modal-header");
    els.title = make(CFG.titleTag, null);
    head.appendChild(els.title);
    var body = make("div", "modal-body");
    els.message = make("p", null);
    els.input = make("input", "crud-input form-control");
    els.input.type = "text";
    els.input.hidden = true;
    body.appendChild(els.message);
    body.appendChild(els.input);
    var foot = make("div", "modal-footer");
    els.cancel = make("button", "btn btn-secondary");
    els.cancel.type = "button";
    els.ok = make("button", "btn btn-danger");
    els.ok.type = "button";
    foot.appendChild(els.cancel);
    foot.appendChild(els.ok);
    box.appendChild(head);
    box.appendChild(body);
    box.appendChild(foot);
    overlay.appendChild(box);
    document.body.appendChild(overlay);

    els.cancel.addEventListener("click", function () { close(false); });
    els.ok.addEventListener("click", function () { close(true); });
    overlay.addEventListener("click", function (e) {
      if (e.target === overlay) close(false);
    });
    els.input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        close(true);
      }
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && current) close(false);
    });
  }

  function close(accepted) {
    if (!current) return;
    var job = current;
    current = null;
    overlay.classList.remove(CFG.open);
    var value = els.input.value;
    if (lastFocus && lastFocus.focus) {
      try { lastFocus.focus(); } catch (err) { console.warn("dialogs: focus restore failed", err); }
    }
    if (accepted && job.onAccept) {
      try {
        job.onAccept(job.prompt ? value : undefined);
      } catch (err) {
        console.error("dialogs: action failed", err);
      }
    } else if (!accepted && job.onCancel) {
      job.onCancel();
    }
  }

  function show(job) {
    build();
    if (current) close(false);
    current = job;
    lastFocus = document.activeElement;
    els.title.textContent = job.title;
    els.message.textContent = job.message;
    els.input.hidden = !job.prompt;
    els.input.value = job.prompt ? (job.value || "") : "";
    els.cancel.hidden = !!job.alertOnly;
    els.cancel.textContent = data("i18nDlgCancel");
    els.ok.textContent = job.actionLabel;
    els.ok.className = "btn " + (job.danger ? "btn-danger" : "btn-primary");
    overlay.classList.add(CFG.open);
    if (job.prompt) {
      els.input.focus();
      els.input.select();
    } else {
      els.ok.focus();
    }
  }

  window.appConfirm = function (message, onConfirm, options) {
    var opts = options || {};
    show({
      title: opts.title || data("i18nDlgConfirmTitle"),
      message: message,
      actionLabel: opts.actionLabel || data("i18nDlgConfirmButton"),
      danger: opts.danger !== false,
      onAccept: onConfirm,
      onCancel: opts.onCancel
    });
  };

  window.appPrompt = function (message, defaultValue, onSubmit, options) {
    var opts = options || {};
    show({
      title: opts.title || data("i18nDlgConfirmTitle"),
      message: message,
      actionLabel: opts.actionLabel || data("i18nDlgConfirmButton"),
      danger: false,
      prompt: true,
      value: defaultValue,
      onAccept: onSubmit,
      onCancel: opts.onCancel
    });
  };

  window.appAlert = function (message, options) {
    var opts = options || {};
    show({
      title: opts.title || data("i18nDlgAlertTitle"),
      message: message,
      actionLabel: data("i18nDlgOk"),
      danger: false,
      alertOnly: true
    });
  };

  window.appConfirmForm = function (e, form, message) {
    if (form.dataset.dlgConfirmed === "1") {
      delete form.dataset.dlgConfirmed;
      return true;
    }
    e.preventDefault();
    var submitter = e.submitter;
    window.appConfirm(message, function () {
      form.dataset.dlgConfirmed = "1";
      if (form.requestSubmit) {
        if (submitter) form.requestSubmit(submitter);
        else form.requestSubmit();
      } else {
        form.submit();
      }
    });
    return false;
  };
})();
