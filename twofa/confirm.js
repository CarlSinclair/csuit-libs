(function () {
  var cancelClass = (document.currentScript && document.currentScript.getAttribute("data-cancel-class")) || "btn btn-secondary";
  document.querySelectorAll("[data-forgot-send]").forEach(function (btn) {
    var out = null;
    btn.addEventListener("click", function () {
      if (!out) {
        out = document.createElement("small");
        out.setAttribute("aria-live", "polite");
        btn.insertAdjacentElement("afterend", out);
      }
      btn.disabled = true;
      var target = btn.getAttribute("data-forgot-send");
      var from = btn.getAttribute("data-query-from");
      if (from) {
        var src = document.querySelector(from);
        target += (target.indexOf("?") === -1 ? "?" : "&") + (btn.getAttribute("data-query-name") || "value") + "=" + encodeURIComponent(src ? src.value : "");
      }
      fetch(target, { credentials: "same-origin", cache: "no-store" })
        .then(function (r) {
          btn.disabled = false;
          var key = r.ok ? "data-text-sent" : r.status === 429 ? "data-text-wait" : "data-text-failed";
          out.textContent = btn.getAttribute(key) || "";
        })
        .catch(function (err) {
          btn.disabled = false;
          console.warn("email code request failed", err);
          out.textContent = btn.getAttribute("data-text-failed") || "";
        });
    });
  });
})();
(function () {
  var forms = document.querySelectorAll("form[data-tfa-confirm]");
  if (!forms.length) return;
  var cfg = null;
  var base = "";
  var dialog = null;
  var statusEl = null;
  var titleEl = null;
  var choices = null;
  var timer = null;
  var run = 0;
  var steps = [];
  var current = null;

  function make(tag, cls) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    return n;
  }

  function getJSON(path, headers) {
    return fetch(base + path, { credentials: "same-origin", cache: "no-store", headers: headers || {} })
      .then(function (r) {
        return r.json().then(function (j) {
          return { ok: r.ok, status: r.status, j: j || {} };
        }, function () {
          return { ok: r.ok, status: r.status, j: {} };
        });
      });
  }

  function build() {
    dialog = make("dialog", "tfa-dialog");
    titleEl = make("h3");
    statusEl = make("p");
    choices = make("div");
    choices.hidden = true;
    var cancel = make("button", cancelClass);
    cancel.type = "button";
    cancel.textContent = cfg.t.cancel;
    cancel.addEventListener("click", abort);
    dialog.addEventListener("cancel", abort);
    dialog.append(titleEl, statusEl, choices, cancel);
    document.body.appendChild(dialog);
  }

  function stop() {
    run++;
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  }

  function abort() {
    stop();
    current = null;
    if (dialog && dialog.open) dialog.close();
  }

  function say(key) {
    statusEl.textContent = cfg.t[key] || "";
  }

  function submitForm(form, submitter) {
    abort();
    form.dataset.tfaOk = "1";
    if (form.requestSubmit) {
      form.requestSubmit(submitter || undefined);
    } else {
      form.submit();
    }
    delete form.dataset.tfaOk;
  }

  function stepDone(name) {
    if (!current) return;
    var ctx = current;
    ctx.done[name] = true;
    if (!cfg.all) {
      submitForm(ctx.form, ctx.submitter);
      return;
    }
    var left = steps.filter(function (s) {
      return !ctx.done[s];
    });
    if (!left.length) {
      submitForm(ctx.form, ctx.submitter);
      return;
    }
    startStep(left[0]);
  }

  function poll(token, secret, mine, name) {
    var tries = 0;
    var busy = false;
    timer = setInterval(function () {
      if (mine !== run || busy) return;
      tries++;
      if (tries > 150) {
        clearInterval(timer);
        timer = null;
        say("expired");
        return;
      }
      busy = true;
      getJSON("/poll?t=" + encodeURIComponent(token), { "X-Login-Secret": secret })
        .then(function (res) {
          busy = false;
          if (mine !== run) return;
          if (!res.ok) {
            clearInterval(timer);
            timer = null;
            say("failed");
          } else if (res.j.status === "approved") {
            clearInterval(timer);
            timer = null;
            stepDone(name);
          } else if (res.j.status === "denied" || res.j.status === "expired") {
            clearInterval(timer);
            timer = null;
            say(res.j.status);
          }
        })
        .catch(function (err) {
          busy = false;
          console.warn("approval check failed", err);
        });
    }, 2000);
  }

  function startStep(name) {
    stop();
    var mine = run;
    choices.hidden = true;
    say(name === "email" ? "email_wait" : "waiting");
    getJSON(name === "email" ? "/email/send" : "/start")
      .then(function (res) {
        if (mine !== run) return;
        if (res.status === 429) {
          say("email_wait");
          return;
        }
        if (!res.ok) throw new Error("status " + res.status);
        say(name === "email" ? "email_sent" : "waiting");
        poll(res.j.token, res.j.secret, mine, name);
      })
      .catch(function (err) {
        if (mine !== run) return;
        console.warn("approval request failed", err);
        say(name === "email" ? "email_failed" : "failed");
      });
  }

  function begin(form, submitter) {
    if (!dialog) build();
    stop();
    steps = [];
    if (cfg.telegram) steps.push("telegram");
    if (cfg.email) steps.push("email");
    current = { form: form, submitter: submitter, done: {} };
    titleEl.textContent = cfg.t.title;
    statusEl.textContent = "";
    choices.hidden = true;
    choices.replaceChildren();
    if (!dialog.open) dialog.showModal();
    if (cfg.all || steps.length === 1) {
      startStep(steps[0]);
      return;
    }
    steps.forEach(function (name) {
      var b = make("button", "btn");
      b.type = "button";
      b.textContent = name === "email" ? cfg.t.email_send : cfg.t.telegram_send;
      b.addEventListener("click", function () {
        startStep(name);
      });
      choices.append(b);
    });
    choices.hidden = false;
  }

  function wire(form, c) {
    if (!c.all) {
      form.querySelectorAll('input[name="totp_code"]').forEach(function (i) {
        i.required = false;
      });
    }
    form.addEventListener("submit", function (e) {
      if (form.dataset.tfaOk === "1") return;
      var totp = form.querySelector('input[name="totp_code"]');
      if (!c.all && totp && totp.value.trim() !== "") return;
      e.preventDefault();
      e.stopImmediatePropagation();
      cfg = c;
      base = form.getAttribute("data-tfa-confirm") || "/2fa/confirm";
      begin(form, e.submitter);
    }, true);
  }

  var loaded = {};
  forms.forEach(function (form) {
    var path = form.getAttribute("data-tfa-confirm") || "/2fa/confirm";
    if (!loaded[path]) {
      loaded[path] = fetch(path + "/config", { credentials: "same-origin", cache: "no-store" })
        .then(function (r) {
          if (!r.ok) throw new Error("status " + r.status);
          return r.json();
        })
        .catch(function (err) {
          console.warn("verification settings unavailable", err);
          return null;
        });
    }
    loaded[path].then(function (c) {
      if (c && (c.telegram || c.email)) wire(form, c);
    });
  });
})();
