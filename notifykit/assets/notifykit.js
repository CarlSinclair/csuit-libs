(function () {
  "use strict";
  var POLL_MS = 30000;
  var FAIL_LIMIT = 3;
  var CONFIRM_MS = 15000;

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = text;
    return n;
  }

  function Bell(root) {
    this.root = root;
    this.prefix = root.getAttribute("data-prefix") || "";
    this.csrf = root.getAttribute("data-csrf") || "";
    this.state = { grouped: false, history: false, summary: null };
    this.modal = { category: "", page: 1 };
    this.timer = null;
    this.failures = 0;
    this.confirming = false;
    this.build();
    this.refresh();
    this.schedule();
  }

  Bell.prototype.label = function (key) {
    return this.root.getAttribute("data-l-" + key) || "";
  };

  Bell.prototype.catLabel = function (cat) {
    return this.root.getAttribute("data-c-" + cat) || this.root.getAttribute("data-c-other") || cat;
  };

  Bell.prototype.fmt = function (key, n) {
    return this.label(key).replace("{n}", String(n));
  };

  Bell.prototype.build = function () {
    var self = this;
    this.button = el("button", "nk-button");
    this.button.type = "button";
    this.button.setAttribute("aria-label", this.label("title"));
    this.button.setAttribute("aria-expanded", "false");
    var icon = this.root.querySelector(".nk-icon");
    if (icon) this.button.appendChild(icon);
    this.badge = el("span", "nk-badge");
    this.badge.hidden = true;
    this.button.appendChild(this.badge);
    this.panel = el("div", "nk-panel");
    this.panel.hidden = true;
    this.root.appendChild(this.button);
    this.root.appendChild(this.panel);
    this.overlay = el("div", "nk-overlay");
    this.overlay.hidden = true;
    this.dialog = el("div", "nk-dialog");
    this.dialog.setAttribute("role", "dialog");
    this.dialog.setAttribute("aria-modal", "true");
    this.overlay.appendChild(this.dialog);
    document.body.appendChild(this.overlay);
    this.button.addEventListener("click", function (e) {
      e.stopPropagation();
      self.togglePanel();
    });
    document.addEventListener("click", function (e) {
      if (!self.panel.hidden && !self.root.contains(e.target)) self.closePanel();
    });
    this.overlay.addEventListener("click", function (e) {
      if (e.target === self.overlay) self.closeModal();
    });
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      if (!self.overlay.hidden) self.closeModal();
      else if (!self.panel.hidden) self.closePanel();
    });
    window.addEventListener("notifykit:refresh", function () {
      self.refresh();
    });
    document.addEventListener("visibilitychange", function () {
      if (!document.hidden) self.refresh();
    });
  };

  Bell.prototype.schedule = function () {
    var self = this;
    if (this.timer) clearInterval(this.timer);
    this.timer = setInterval(function () {
      if (!document.hidden) self.refresh();
    }, POLL_MS);
  };

  Bell.prototype.request = function (method, path, body) {
    var opts = { method: method, credentials: "same-origin", headers: { "X-CSRF-Token": this.csrf, "Accept": "application/json" } };
    if (body) {
      opts.headers["Content-Type"] = "application/x-www-form-urlencoded";
      opts.body = body;
    }
    return fetch(this.prefix + path, opts).then(function (res) {
      if (!res.ok) throw new Error("http " + res.status);
      var type = res.headers.get("Content-Type") || "";
      if (type.indexOf("json") === -1) {
        var err = new Error("not json: " + (res.redirected ? "redirected to " + res.url : type || "no content type"));
        err.signedOut = res.redirected;
        throw err;
      }
      return res.json();
    });
  };

  Bell.prototype.refresh = function () {
    var self = this;
    return this.request("GET", "/summary").then(function (data) {
      self.failures = 0;
      self.state.summary = data;
      self.renderBadge();
      if (!self.panel.hidden) self.renderPanel();
    }).catch(function (err) {
      if (err && err.signedOut) {
        if (self.timer) clearInterval(self.timer);
        self.timer = null;
        self.state.summary = null;
        self.renderBadge();
        return;
      }
      self.failures += 1;
      if (self.failures < FAIL_LIMIT || self.confirming) return;
      self.confirming = true;
      setTimeout(function () {
        self.request("GET", "/summary").then(function (data) {
          self.confirming = false;
          self.failures = 0;
          self.state.summary = data;
          self.renderBadge();
          if (!self.panel.hidden) self.renderPanel();
        }).catch(function (again) {
          self.confirming = false;
          if (again && again.signedOut) {
            if (self.timer) clearInterval(self.timer);
            self.timer = null;
            self.state.summary = null;
            self.renderBadge();
            return;
          }
          console.error("notifications: summary failed", again);
          self.state.summary = null;
          if (!self.panel.hidden) self.renderError(self.panel);
        });
      }, CONFIRM_MS);
    });
  };

  Bell.prototype.renderBadge = function () {
    var n = this.state.summary ? this.state.summary.unread : 0;
    this.badge.hidden = n <= 0;
    this.badge.textContent = n > 99 ? "99+" : String(n);
    this.root.classList.toggle("nk-has-unread", n > 0);
  };

  Bell.prototype.togglePanel = function () {
    if (this.panel.hidden) this.openPanel();
    else this.closePanel();
  };

  Bell.prototype.openPanel = function () {
    this.panel.hidden = false;
    this.button.setAttribute("aria-expanded", "true");
    this.state.history = false;
    this.renderPanel();
    this.refresh();
  };

  Bell.prototype.closePanel = function () {
    this.panel.hidden = true;
    this.button.setAttribute("aria-expanded", "false");
  };

  Bell.prototype.renderError = function (target) {
    target.textContent = "";
    target.appendChild(el("div", "nk-error", this.label("error")));
  };

  Bell.prototype.itemNode = function (it, compact) {
    var self = this;
    var a = el("a", "nk-item" + (it.read ? " nk-read" : " nk-unread"));
    a.href = it.open_url;
    if (!it.has_link) {
      a.addEventListener("click", function (e) {
        e.preventDefault();
        if (it.read) return;
        self.request("POST", "/read/" + it.id).then(function () {
          it.read = true;
          a.classList.remove("nk-unread");
          a.classList.add("nk-read");
          self.refresh();
        }).catch(function (err) {
          console.error("notifications: mark read failed", err);
        });
      });
    }
    a.appendChild(el("span", "nk-item-title", it.title));
    if (it.body && !compact) a.appendChild(el("span", "nk-item-body", it.body));
    var meta = el("span", "nk-item-meta", this.catLabel(it.category) + " · " + this.when(it.created_at));
    a.appendChild(meta);
    if (!it.extra) return a;
    var row = el("div", "nk-row");
    row.appendChild(a);
    window.dispatchEvent(new CustomEvent("notifykit:item", { detail: { item: it, row: row } }));
    return row;
  };

  Bell.prototype.when = function (iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    try {
      return d.toLocaleString(document.documentElement.lang || undefined, { dateStyle: "medium", timeStyle: "short" });
    } catch (e) {
      return d.toLocaleString();
    }
  };

  Bell.prototype.categoryRow = function (c) {
    var self = this;
    var b = el("button", "nk-group" + (c.unread > 0 ? " nk-unread" : " nk-read"));
    b.type = "button";
    b.appendChild(el("span", "nk-group-name", this.catLabel(c.category)));
    if (c.unread > 0) b.appendChild(el("span", "nk-group-count", String(c.unread)));
    if (c.latest_title) b.appendChild(el("span", "nk-item-meta", c.latest_title));
    b.addEventListener("click", function () {
      self.openModal(c.category);
    });
    return b;
  };

  Bell.prototype.renderPanel = function () {
    var self = this;
    var s = this.state.summary;
    var p = this.panel;
    p.textContent = "";
    var head = el("div", "nk-head");
    head.appendChild(el("strong", "nk-head-title", this.label("title")));
    p.appendChild(head);
    if (!s) {
      this.renderError(p);
      return;
    }
    var body = el("div", "nk-body");
    var showGroups = s.grouped || this.state.history;
    if (showGroups) {
      var cats = (s.categories || []).filter(function (c) { return c.total > 0 || c.unread > 0; });
      if (!cats.length) body.appendChild(el("div", "nk-empty", this.label("empty")));
      cats.forEach(function (c) { body.appendChild(self.categoryRow(c)); });
    } else if (s.items && s.items.length) {
      s.items.forEach(function (it) { body.appendChild(self.itemNode(it, true)); });
    } else {
      body.appendChild(el("div", "nk-empty", this.label("empty")));
    }
    p.appendChild(body);
    if (!s.grouped) {
      var foot = el("div", "nk-foot");
      var t = el("button", "nk-link", this.state.history ? this.label("back") : this.label("history"));
      t.type = "button";
      t.addEventListener("click", function () {
        self.state.history = !self.state.history;
        self.renderPanel();
      });
      foot.appendChild(t);
      p.appendChild(foot);
    }
  };

  Bell.prototype.openModal = function (category) {
    this.modal = { category: category, page: 1 };
    this.closePanel();
    this.overlay.hidden = false;
    this.loadModal();
  };

  Bell.prototype.closeModal = function () {
    this.overlay.hidden = true;
    this.dialog.textContent = "";
    this.refresh();
  };

  Bell.prototype.loadModal = function () {
    var self = this;
    var q = "?category=" + encodeURIComponent(this.modal.category) + "&page=" + this.modal.page;
    this.request("GET", "/list" + q).then(function (data) {
      self.renderModal(data);
    }).catch(function (err) {
      console.error("notifications: list failed", err);
      self.dialog.textContent = "";
      self.dialog.appendChild(self.modalHead(self.catLabel(self.modal.category), 0));
      self.renderError(self.dialog);
    });
  };

  Bell.prototype.modalHead = function (title, unread) {
    var self = this;
    var head = el("div", "nk-modal-head");
    head.appendChild(el("h2", "nk-modal-title", title));
    var x = el("button", "nk-close", "×");
    x.type = "button";
    x.setAttribute("aria-label", this.label("close"));
    x.addEventListener("click", function () { self.closeModal(); });
    head.appendChild(x);
    return head;
  };

  Bell.prototype.renderModal = function (data) {
    var self = this;
    var d = this.dialog;
    d.textContent = "";
    d.appendChild(this.modalHead(this.catLabel(data.category), data.unread));
    if (data.unread > 0) {
      var bar = el("div", "nk-modal-bar");
      var mark = el("button", "nk-btn", this.label("markgroup"));
      mark.type = "button";
      mark.addEventListener("click", function () {
        mark.disabled = true;
        self.request("POST", "/read-category", "category=" + encodeURIComponent(data.category)).then(function () {
          self.loadModal();
          self.refresh();
        }).catch(function (err) {
          console.error("notifications: mark category failed", err);
          mark.disabled = false;
          var e = d.querySelector(".nk-error");
          if (!e) bar.appendChild(el("div", "nk-error", self.label("error")));
        });
      });
      bar.appendChild(mark);
      d.appendChild(bar);
    }
    var list = el("div", "nk-modal-list");
    if (!data.items || !data.items.length) list.appendChild(el("div", "nk-empty", this.label("empty")));
    (data.items || []).forEach(function (it) { list.appendChild(self.itemNode(it, false)); });
    d.appendChild(list);
    if (data.pages > 1) {
      var pager = el("div", "nk-pager");
      var prev = el("button", "nk-btn", this.label("prev"));
      prev.type = "button";
      prev.disabled = data.page <= 1;
      prev.addEventListener("click", function () { self.modal.page = data.page - 1; self.loadModal(); });
      var next = el("button", "nk-btn", this.label("next"));
      next.type = "button";
      next.disabled = data.page >= data.pages;
      next.addEventListener("click", function () { self.modal.page = data.page + 1; self.loadModal(); });
      pager.appendChild(prev);
      pager.appendChild(el("span", "nk-page", data.page + " / " + data.pages));
      pager.appendChild(next);
      d.appendChild(pager);
    }
  };

  function init() {
    var nodes = document.querySelectorAll("[data-notifykit]");
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i].getAttribute("data-nk-ready")) continue;
      nodes[i].setAttribute("data-nk-ready", "1");
      new Bell(nodes[i]);
    }
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
