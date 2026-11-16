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
      fetch(btn.getAttribute("data-forgot-send"), { credentials: "same-origin", cache: "no-store" })
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
  var emailBox = null;
  var emailInput = null;
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
    emailBox = make("div");
    emailBox.hidden = true;
    emailInput = make("input");
    emailInput.type = "text";
    emailInput.inputMode = "numeric";
    emailInput.autocomplete = "one-time-code";
    emailInput.placeholder = cfg.t.email_label;
    var send = make("button", "btn");
    send.type = "button";
    send.textContent = cfg.t.email_send;
    send.addEventListener("click", sendEmail);
    var verify = make("button", "btn");
    verify.type = "button";
    verify.textContent = cfg.t.email_verify;
    verify.addEventListener("click", verifyEmail);
    emailInput.addEventListener("keydown", function (e) {
      if (e.key === "Enter") {
        e.preventDefault();
        verifyEmail();
      }
    });
    emailBox.append(emailInput, send, verify);
    var cancel = make("button", cancelClass);
    cancel.type = "button";
    cancel.textContent = cfg.t.cancel;
    cancel.addEventListener("click", abort);
    dialog.addEventListener("cancel", abort);
    dialog.append(titleEl, statusEl, emailBox, cancel);
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

  function poll(token, secret, mine) {
    var tries = 0;
    var busy = false;
    timer = setInterval(function () {
      if (mine !== run || busy) return;
      tries++;
      if (tries > 60) {
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
            stepDone("telegram");
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

  function sendEmail() {
    var mine = run;
    say("email_wait");
    getJSON("/email/send")
      .then(function (res) {
        if (mine !== run) return;
        if (res.ok) {
          say("email_sent");
          emailInput.focus();
        } else if (res.status === 429) {
          say("email_wait");
        } else {
          say("email_failed");
        }
      })
      .catch(function (err) {
        if (mine !== run) return;
        console.warn("email code request failed", err);
        say("email_failed");
      });
  }

  function verifyEmail() {
    var mine = run;
    var code = emailInput.value.trim();
    if (!code) return;
    getJSON("/email/verify", { "X-Email-Code": code })
      .then(function (res) {
        if (mine !== run) return;
        if (res.ok && res.j.status === "approved") {
          stepDone("email");
        } else if (res.ok) {
          say("email_bad");
        } else {
          say("email_failed");
        }
      })
      .catch(function (err) {
        if (mine !== run) return;
        console.warn("email code check failed", err);
        say("email_failed");
      });
  }

  function startTelegram() {
    var mine = run;
    say("waiting");
    getJSON("/start")
      .then(function (res) {
        if (mine !== run) return;
        if (!res.ok) throw new Error("status " + res.status);
        poll(res.j.token, res.j.secret, mine);
      })
      .catch(function (err) {
        if (mine !== run) return;
        console.warn("approval request failed", err);
        say("failed");
      });
  }

  function startStep(name) {
    if (name === "telegram") {
      emailBox.hidden = true;
      startTelegram();
    } else {
      emailBox.hidden = false;
      emailInput.value = "";
      sendEmail();
    }
  }

  function begin(form, submitter) {
    if (!dialog) build();
    stop();
    steps = [];
    if (cfg.telegram) steps.push("telegram");
    if (cfg.email) steps.push("email");
    current = { form: form, submitter: submitter, done: {} };
    titleEl.textContent = cfg.telegram ? cfg.t.title : cfg.t.email_send;
    statusEl.textContent = "";
    emailBox.hidden = true;
    if (!dialog.open) dialog.showModal();
    if (cfg.all) {
      startStep(steps[0]);
      return;
    }
    if (cfg.telegram) {
      startTelegram();
    }
    if (cfg.email) {
      emailBox.hidden = false;
      emailInput.value = "";
      if (!cfg.telegram) sendEmail();
    }
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
      var ec = form.querySelector('input[name="email_code"]');
      var eff = c;
      if (ec && ec.value.trim() !== "") {
        if (c.email && !c.all) return;
        eff = Object.assign({}, c, { email: false });
        if (!eff.telegram) return;
      }
      e.preventDefault();
      e.stopImmediatePropagation();
      cfg = eff;
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
