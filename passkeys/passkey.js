(function () {
  if (window.__passkeyBound) { return; }
  window.__passkeyBound = true;

  function supported() {
    return !!(window.PublicKeyCredential && PublicKeyCredential.parseCreationOptionsFromJSON && PublicKeyCredential.parseRequestOptionsFromJSON);
  }

  function post(url, csrf, body) {
    var headers = { 'X-CSRF-Token': csrf, 'Accept': 'application/json', 'Content-Type': 'application/json' };
    var stay = document.querySelector('select[name="stay"]');
    if (stay && stay.value) { headers['X-Stay'] = stay.value; }
    return fetch(url, {
      method: 'POST',
      credentials: 'same-origin',
      headers: headers,
      body: body ? JSON.stringify(body) : '{}'
    }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) { throw new Error(j.error || 'failed'); }
        return j;
      });
    });
  }

  function show(el, msg) {
    if (!el) { return; }
    el.textContent = msg;
    el.hidden = false;
  }

  function failure(btn, err, e) {
    btn.disabled = false;
    show(err, e && e.name === 'NotAllowedError' ? btn.getAttribute('data-msg-cancelled') : btn.getAttribute('data-msg-error'));
  }

  function refreshState() {
    var ok = supported();
    Array.prototype.forEach.call(document.querySelectorAll('[data-passkey-login]'), function (btn) {
      if (!ok) { btn.hidden = true; }
    });
    var add = document.getElementById('passkey-add');
    if (add && !ok && !add.disabled) {
      add.disabled = true;
      add.title = add.getAttribute('data-msg-unsupported') || '';
    }
  }

  function login(btn) {
    var err = btn.parentNode.querySelector('[data-passkey-error]');
    var csrf = btn.getAttribute('data-csrf') || '';
    btn.disabled = true;
    if (err) { err.hidden = true; }
    var token = '';
    post('/auth/passkey/begin', csrf).then(function (j) {
      token = j.token;
      return navigator.credentials.get({ publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(j.options.publicKey) });
    }).then(function (cred) {
      return post('/auth/passkey/finish?t=' + encodeURIComponent(token), csrf, cred.toJSON());
    }).then(function (j) {
      window.location.href = j.redirect;
    }).catch(function (e) { failure(btn, err, e); });
  }

  function register(add, name) {
    var err = document.getElementById('passkey-error');
    var csrf = add.getAttribute('data-csrf') || '';
    add.disabled = true;
    if (err) { err.hidden = true; }
    post(add.getAttribute('data-begin'), csrf).then(function (j) {
      return navigator.credentials.create({ publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(j.options.publicKey) });
    }).then(function (cred) {
      return post(add.getAttribute('data-finish') + '?name=' + encodeURIComponent(name), csrf, cred.toJSON());
    }).then(function () {
      if (typeof window.cshopSoftRefresh === 'function') { return window.cshopSoftRefresh(); }
      window.location.reload();
    }).catch(function (e) { failure(add, err, e); });
  }

  function addPasskey(add) {
    if (typeof window.appPrompt !== 'function') { register(add, ''); return; }
    window.appPrompt(add.getAttribute('data-msg-name') || '', add.getAttribute('data-default-name') || '', function (value) {
      register(add, String(value || '').trim().slice(0, 60));
    });
  }

  document.addEventListener('click', function (e) {
    var t = e.target;
    if (!t || !t.closest) { return; }
    var btn = t.closest('[data-passkey-login]');
    if (btn && supported() && !btn.disabled) { e.preventDefault(); login(btn); return; }
    var add = t.closest('#passkey-add');
    if (add && supported() && !add.disabled) { e.preventDefault(); addPasskey(add); }
  });

  refreshState();
  new MutationObserver(refreshState).observe(document.documentElement, { childList: true, subtree: true });
})();
