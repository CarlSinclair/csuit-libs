(function () {
  var timer = null;
  var trigger = null;

  function stayHeaders(secret) {
    var h = { "X-Login-Secret": secret };
    var stay = document.querySelector('select[name="stay"]');
    if (stay && stay.value) h["X-Stay"] = stay.value;
    return h;
  }

  function say(key) {
    var status = document.querySelector("[data-tfa-status]");
    if (!status || !trigger) return;
    status.textContent = trigger.getAttribute("data-msg-" + key) || "";
    status.hidden = false;
  }

  function stop() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
    if (trigger) trigger.disabled = false;
  }

  function poll(token, secret) {
    var tries = 0;
    var busy = false;
    var done = false;
    timer = setInterval(function () {
      if (!trigger || !trigger.isConnected) {
        done = true;
        clearInterval(timer);
        timer = null;
        return;
      }
      if (done || busy) return;
      tries++;
      if (tries > 60) {
        done = true;
        stop();
        say("expired");
        return;
      }
      busy = true;
      var pollURL = trigger.getAttribute("data-poll");
      fetch(pollURL + (pollURL.indexOf("?") === -1 ? "?" : "&") + "t=" + encodeURIComponent(token), {
        credentials: "same-origin",
        cache: "no-store",
        headers: stayHeaders(secret)
      })
        .then(function (r) {
          return r.json().then(function (j) {
            return { ok: r.ok, j: j };
          });
        })
        .then(function (res) {
          if (done) return;
          var p = res.j || {};
          if (!res.ok) {
            done = true;
            stop();
            say(p.error === "session_expired" || p.error === "error_session_expired" || p.key === "err.mfa_expired" ? "expired" : "error");
          } else if (p.status === "approved" && p.next) {
            done = true;
            clearInterval(timer);
            window.location.href = p.next;
          } else if (p.status === "more") {
            done = true;
            clearInterval(timer);
            window.location.reload();
          } else if (p.status === "denied") {
            done = true;
            stop();
            say("denied");
          } else if (p.status === "expired") {
            done = true;
            stop();
            say("expired");
          }
        })
        .catch(function () {})
        .then(function () {
          busy = false;
        });
    }, 2500);
  }

  document.addEventListener("click", function (e) {
    var t = e.target && e.target.closest ? e.target.closest("[data-tfa-telegram]") : null;
    if (!t || t.disabled) return;
    stop();
    trigger = t;
    trigger.disabled = true;
    say("waiting");
    fetch(trigger.getAttribute("data-start"), { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        return r.json().then(function (j) {
          return { ok: r.ok, j: j };
        });
      })
      .then(function (res) {
        if (!res.ok || !res.j.token) throw new Error("start");
        poll(res.j.token, res.j.secret);
      })
      .catch(function () {
        stop();
        say("error");
      });
  });
})();
