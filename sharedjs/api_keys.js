(function () {
  if (window.__apiKeysCleanup) {
    window.__apiKeysCleanup();
    window.__apiKeysCleanup = null;
  }
  var root = document.querySelector("[data-api-root]");
  if (!root) {
    return;
  }
  var BASE = root.getAttribute("data-base") || "/account/api";
  var BOT_ID = root.getAttribute("data-bot-id") || "";
  var TRIGGER = root.getAttribute("data-api-trigger") || "";
  var csrfMeta = document.querySelector('meta[name="csrf-token"]');
  var csrf = csrfMeta ? csrfMeta.getAttribute("content") : "";
  var msgEl = document.getElementById("api-messages");
  var body = document.getElementById("api-body");
  var statusEl = document.getElementById("api-status");
  var state = { loaded: false, loading: false };

  function msg(name) {
    return msgEl ? msgEl.getAttribute("data-msg-" + name) || "" : "";
  }

  function errText(code) {
    return msg("err-" + code) || msg("err-generic");
  }

  function setStatus(text, isError) {
    statusEl.textContent = text || "";
    statusEl.classList.toggle("form-error", !!isError);
    statusEl.hidden = !text;
  }

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) {
      n.className = cls;
    }
    if (text !== undefined && text !== null) {
      n.textContent = text;
    }
    return n;
  }

  function request(method, path, payload) {
    var opts = { method: method, credentials: "same-origin", headers: { Accept: "application/json" } };
    if (method !== "GET") {
      opts.headers["X-CSRF-Token"] = csrf;
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(payload || {});
    }
    var url = BASE + path;
    if (BOT_ID) {
      url += (path.indexOf("?") >= 0 ? "&" : "?") + "bot_id=" + encodeURIComponent(BOT_ID);
    }
    return fetch(url, opts).then(function (res) {
      return res.text().then(function (txt) {
        var data = null;
        if (txt) {
          try {
            data = JSON.parse(txt);
          } catch (e) {
            data = null;
          }
        }
        if (!res.ok) {
          var code = data && data.error && data.error.code ? data.error.code : "generic";
          if (res.status === 429) {
            code = "rate_limited";
          }
          if (res.status === 403 && code === "generic") {
            code = "forbidden";
          }
          var err = new Error(code);
          err.code = code;
          throw err;
        }
        return data;
      });
    });
  }

  function fail(err) {
    setStatus(errText(err && err.code ? err.code : "generic"), true);
  }

  function fmtDate(v) {
    if (!v) {
      return "";
    }
    var d = new Date(v);
    return isNaN(d.getTime()) ? "" : d.toLocaleString();
  }

  function checkbox(name, value, checked) {
    var label = el("label", "flex gap-10 items-center");
    var input = document.createElement("input");
    input.type = "checkbox";
    input.name = name;
    input.value = value;
    input.checked = !!checked;
    label.appendChild(input);
    label.appendChild(el("code", "", value));
    return label;
  }

  function button(cls, text, handler) {
    var b = el("button", cls, text);
    b.type = "button";
    b.addEventListener("click", handler);
    return b;
  }

  function secretBox(text, noticeKey) {
    var box = el("div", "form-group");
    box.appendChild(el("p", "", msg(noticeKey)));
    var input = document.createElement("input");
    input.type = "text";
    input.readOnly = true;
    input.value = text;
    input.className = "crud-input";
    box.appendChild(input);
    box.appendChild(button("btn btn-secondary btn-sm", msg("copy"), function () {
      input.select();
      var done = function () {
        setStatus(msg("copied"), false);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () {
          setStatus(msg("err-generic"), true);
        });
      } else {
        try {
          document.execCommand("copy");
          done();
        } catch (e) {
          setStatus(msg("err-generic"), true);
        }
      }
    }));
    return box;
  }

  function section(title, onAdd) {
    var wrap = el("section", "form-group");
    var head = el("div", "modal-header flex flex-between items-center gap-10");
    head.appendChild(el("h3", "mt-0", title));
    var add = el("button", "btn-add");
    add.type = "button";
    add.title = msg("add");
    add.setAttribute("aria-label", msg("add"));
    add.appendChild(el("span", "", "+"));
    head.appendChild(add);
    wrap.appendChild(head);
    var holder = el("div", "");
    wrap.appendChild(holder);
    var formHost = el("form", "form");
    formHost.hidden = true;
    holder.appendChild(formHost);
    add.addEventListener("click", function () {
      formHost.hidden = !formHost.hidden;
    });
    onAdd(formHost);
    return { wrap: wrap, holder: holder };
  }

  function keyForm(form, data) {
    var fg = el("div", "form-group");
    fg.appendChild(el("label", "", msg("key-label")));
    var label = document.createElement("input");
    label.type = "text";
    label.maxLength = 100;
    label.className = "crud-input";
    fg.appendChild(label);
    form.appendChild(fg);
    var sg = el("div", "form-group");
    sg.appendChild(el("label", "", msg("key-scopes")));
    var presets = data.presets || [];
    if (presets.length) {
      var pr = el("div", "flex gap-10 items-center");
      pr.appendChild(el("span", "", msg("preset-label")));
      presets.forEach(function (p) {
        pr.appendChild(button("btn btn-secondary", msg("preset-" + p.id) || p.id, function () {
          var want = {};
          (p.scopes || []).forEach(function (s) {
            want[s] = true;
          });
          sg.querySelectorAll('input[name="scope"]').forEach(function (c) {
            c.checked = !!want[c.value];
          });
        }));
      });
      sg.appendChild(pr);
    }
    (data.scopes || []).forEach(function (s) {
      sg.appendChild(checkbox("scope", s, false));
    });
    form.appendChild(sg);
    var eg = el("div", "form-group");
    eg.appendChild(el("label", "", msg("key-expiry")));
    var sel = document.createElement("select");
    sel.className = "crud-input";
    [["0", msg("expiry-never")], ["30", "30 " + msg("days")], ["90", "90 " + msg("days")], ["365", "365 " + msg("days")]].forEach(function (o) {
      var op = document.createElement("option");
      op.value = o[0];
      op.textContent = o[1];
      sel.appendChild(op);
    });
    eg.appendChild(sel);
    form.appendChild(eg);
    var out = el("div", "");
    form.appendChild(out);
    form.appendChild(button("btn btn-primary", msg("create-key"), function () {
      var scopes = [];
      form.querySelectorAll('input[name="scope"]:checked').forEach(function (c) {
        scopes.push(c.value);
      });
      setStatus("", false);
      request("POST", "/keys", { label: label.value.trim(), scopes: scopes, expires_days: parseInt(sel.value, 10) || 0 }).then(function (res) {
        state.created = { type: "key", value: res.token };
        load();
      }).catch(fail);
    }));
  }

  function hookForm(form, data) {
    var fg = el("div", "form-group");
    fg.appendChild(el("label", "", msg("hook-url")));
    var url = document.createElement("input");
    url.type = "url";
    url.className = "crud-input";
    fg.appendChild(url);
    form.appendChild(fg);
    var eg = el("div", "form-group");
    eg.appendChild(el("label", "", msg("hook-events")));
    (data.events || []).forEach(function (e) {
      eg.appendChild(checkbox("event", e, false));
    });
    form.appendChild(eg);
    form.appendChild(button("btn btn-primary", msg("create-hook"), function () {
      var events = [];
      form.querySelectorAll('input[name="event"]:checked').forEach(function (c) {
        events.push(c.value);
      });
      setStatus("", false);
      request("POST", "/webhooks", { url: url.value.trim(), events: events }).then(function (res) {
        state.created = { type: "hook", value: res.secret };
        load();
      }).catch(fail);
    }));
  }

  function renderKeys(parent, data) {
    var s = section(msg("keys-heading"), function (form) {
      keyForm(form, data);
    });
    var list = data.keys || [];
    if (!list.length) {
      s.holder.appendChild(el("p", "opacity-70", msg("no-keys")));
    }
    list.forEach(function (k) {
      var row = el("div", "flex flex-between items-center gap-10");
      var info = el("div", "");
      info.appendChild(el("strong", "", k.label));
      info.appendChild(document.createTextNode(" "));
      info.appendChild(el("code", "", k.prefix + "..."));
      if (k.revoked) {
        info.appendChild(el("span", "badge", " " + msg("revoked")));
      } else if (k.expired) {
        info.appendChild(el("span", "badge", " " + msg("expired")));
      }
      var meta = [];
      meta.push((k.scopes || []).join(", "));
      meta.push(k.last_used_at ? msg("last-used") + " " + fmtDate(k.last_used_at) : msg("never-used"));
      if (k.expires_at) {
        meta.push(msg("expires") + " " + fmtDate(k.expires_at));
      }
      info.appendChild(el("div", "opacity-70", meta.join(" | ")));
      row.appendChild(info);
      if (!k.revoked) {
        row.appendChild(button("btn btn-secondary btn-sm", msg("revoke"), function () {
          (window.ctimeConfirm || window.appConfirm)(msg("confirm-revoke"), function () {
            setStatus("", false);
            request("POST", "/keys/revoke", { id: k.id }).then(load).catch(fail);
          });
        }));
      }
      s.holder.appendChild(row);
    });
    parent.appendChild(s.wrap);
  }

  function renderDeliveries(host, hook) {
    host.textContent = "";
    request("GET", "/deliveries?webhook_id=" + encodeURIComponent(hook.id)).then(function (res) {
      var rows = res && res.data ? res.data : [];
      if (!rows.length) {
        host.appendChild(el("p", "opacity-70", msg("no-deliveries")));
        return;
      }
      rows.forEach(function (d) {
        var line = [fmtDate(d.created_at), d.event_type, d.ok ? msg("ok") : msg("failed"), d.status_code ? String(d.status_code) : "", d.error || ""];
        host.appendChild(el("div", "opacity-70", line.filter(Boolean).join(" | ")));
      });
    }).catch(fail);
  }

  function renderHooks(parent, data) {
    var s = section(msg("hooks-heading"), function (form) {
      hookForm(form, data);
    });
    var list = data.webhooks || [];
    if (!list.length) {
      s.holder.appendChild(el("p", "opacity-70", msg("no-hooks")));
    }
    list.forEach(function (h) {
      var block = el("div", "form-group");
      var row = el("div", "flex flex-between items-center gap-10");
      var info = el("div", "");
      info.appendChild(el("code", "", h.url));
      if (!h.enabled) {
        info.appendChild(el("span", "badge", " " + msg("disabled")));
      }
      info.appendChild(el("div", "opacity-70", (h.events || []).join(", ")));
      row.appendChild(info);
      var actions = el("div", "flex gap-10");
      var log = el("div", "");
      actions.appendChild(button("btn btn-secondary btn-sm", msg("test"), function () {
        setStatus("", false);
        request("POST", "/webhooks/test", { id: h.id }).then(function (d) {
          setStatus(d && d.ok ? msg("test-ok") : msg("test-failed") + (d && d.error ? ": " + d.error : ""), !(d && d.ok));
          renderDeliveries(log, h);
        }).catch(fail);
      }));
      actions.appendChild(button("btn btn-secondary btn-sm", msg("deliveries"), function () {
        renderDeliveries(log, h);
      }));
      actions.appendChild(button("btn btn-secondary btn-sm", h.enabled ? msg("disable") : msg("enable"), function () {
        setStatus("", false);
        request("POST", "/webhooks/toggle", { id: h.id, enabled: !h.enabled }).then(load).catch(fail);
      }));
      actions.appendChild(button("btn btn-secondary btn-sm", msg("delete"), function () {
        (window.ctimeConfirm || window.appConfirm)(msg("confirm-delete"), function () {
          setStatus("", false);
          request("POST", "/webhooks/delete", { id: h.id }).then(load).catch(fail);
        }, { actionLabel: document.body.dataset.i18nConfirmDeleteButton });
      }));
      row.appendChild(actions);
      block.appendChild(row);
      block.appendChild(log);
      s.holder.appendChild(block);
    });
    parent.appendChild(s.wrap);
  }

  function render(data) {
    body.textContent = "";
    if (state.created) {
      body.appendChild(secretBox(state.created.value, state.created.type === "key" ? "key-notice" : "hook-notice"));
      state.created = null;
    }
    var info = el("p", "opacity-70");
    info.appendChild(document.createTextNode(msg("base-url") + " "));
    info.appendChild(el("code", "", data.base_url || ""));
    info.appendChild(document.createTextNode(" | "));
    var link = el("a", "", msg("spec"));
    link.href = (data.base_url || "") + "/openapi.json";
    link.target = "_blank";
    link.rel = "noopener";
    info.appendChild(link);
    body.appendChild(info);
    renderKeys(body, data);
    renderHooks(body, data);
  }

  function load() {
    if (state.loading) {
      return;
    }
    state.loading = true;
    request("GET", "/overview").then(function (data) {
      state.loaded = true;
      render(data);
    }).catch(fail).then(function () {
      state.loading = false;
    });
  }

  function maybeLoad() {
    setStatus("", false);
    load();
  }

  if (TRIGGER === "hash") {
    var onHash = function () {
      if (window.location.hash === "#api") maybeLoad();
    };
    window.addEventListener("hashchange", onHash);
    window.__apiKeysCleanup = function () {
      window.removeEventListener("hashchange", onHash);
    };
    onHash();
  } else if (TRIGGER === "tab") {
    var onTabClick = function (e) {
      var btn = e.target && e.target.closest && e.target.closest('[data-settings-tab-button="api_access"]');
      if (btn) maybeLoad();
    };
    document.addEventListener("click", onTabClick);
    window.__apiKeysCleanup = function () {
      document.removeEventListener("click", onTabClick);
    };
    if (new URLSearchParams(window.location.search).get("tab") === "api_access") maybeLoad();
  } else {
    maybeLoad();
  }
})();
