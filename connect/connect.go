package connect

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const codeTTL = 2 * time.Minute

type User struct {
	ID    int64
	Email string
	Admin bool
}

type Service struct {
	ID              string
	Label           string
	GrantScopes     string
	ScopeLineKeys   []string
	ScopesCannotKey string
	// ClientName is the product on the other end of the connection, used in the
	// key label and as the fallback actor on the consent page.
	ClientName         string
	TrustForwardedHost bool
	LoginPath          string

	CreateKey    func(label, scopes string) (rawKey string, id int64, err error)
	BusinessName func() string
	Language     func(r *http.Request) string
	Translate    func(lang, key string) string
	LocaleDir    func(lang string) string
	CSRFToken    func(r *http.Request) string
	CurrentUser  func(r *http.Request) (User, bool)
	Audit        func(r *http.Request, userID int64, action string, details map[string]interface{})
	Fail         func(w http.ResponseWriter, status int, msg string)
	JSON         func(w http.ResponseWriter, status int, body map[string]any)

	mu     sync.Mutex
	grants map[string]grant
}

type grant struct {
	rawKey   string
	scopes   string
	callback string
	created  time.Time
}

func (c *Service) fail(w http.ResponseWriter, status int, msg string) {
	if c.Fail != nil {
		c.Fail(w, status, msg)
		return
	}
	http.Error(w, msg, status)
}

func (c *Service) writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	if c.JSON != nil {
		c.JSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("connect: writing json response failed service=%s err=%v", c.ID, err)
	}
}

func (c *Service) gc() {
	now := time.Now()
	for k, g := range c.grants {
		if now.Sub(g.created) > codeTTL {
			delete(c.grants, k)
		}
	}
}

func newCode() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func validCallback(raw string) (*url.URL, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, false
	}
	return u, true
}

func (c *Service) hostForDisplay(r *http.Request) string {
	if c.TrustForwardedHost {
		if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
			return h
		}
	}
	return r.Host
}

func (c *Service) baseURL(r *http.Request) string {
	proto := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		proto = "http"
	}
	return proto + "://" + c.hostForDisplay(r)
}

func queryJoin(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

// Authorize renders the consent screen (GET) and, on Approve (POST), mints the
// key and redirects back with a one-time code. Admin session required.
func (c *Service) Authorize(w http.ResponseWriter, r *http.Request) {
	user, ok := c.CurrentUser(r)
	if !ok {
		if r.Method == http.MethodGet && c.LoginPath != "" {
			http.Redirect(w, r, c.LoginPath, http.StatusSeeOther)
			return
		}
		c.fail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if !user.Admin {
		c.fail(w, http.StatusForbidden, "Only an admin can connect this instance")
		return
	}

	get := func(k string) string {
		if r.Method == http.MethodPost {
			return strings.TrimSpace(r.FormValue(k))
		}
		return strings.TrimSpace(r.URL.Query().Get(k))
	}
	callback := get("callback")
	state := get("state")
	clientLabel := get("bot")

	cb, cbOK := validCallback(callback)
	if !cbOK {
		c.fail(w, http.StatusBadRequest, "connect: callback must be an absolute https URL")
		return
	}

	if r.Method == http.MethodGet {
		c.writeConsentPage(w, r, callback, state, clientLabel, user)
		return
	}

	if r.FormValue("approve") != "1" {
		http.Redirect(w, r, callback+queryJoin(callback)+"error=denied&state="+url.QueryEscape(state), http.StatusSeeOther)
		return
	}

	client := strings.TrimSpace(c.ClientName)
	if client == "" {
		client = "Connector"
	}
	label := client + " connector"
	if clientLabel != "" {
		label = client + ": " + clientLabel
	}
	rawKey, id, err := c.CreateKey(label, c.GrantScopes)
	if err != nil {
		logger.Error("connect authorize: mint key service=%s user_id=%d: %v", c.ID, user.ID, err)
		c.fail(w, http.StatusInternalServerError, "connect: could not create key")
		return
	}
	code := newCode()
	if code == "" {
		logger.Error("connect authorize: code generation failed service=%s user_id=%d key_id=%d", c.ID, user.ID, id)
		c.fail(w, http.StatusInternalServerError, "connect: internal error")
		return
	}
	c.mu.Lock()
	if c.grants == nil {
		c.grants = map[string]grant{}
	}
	c.gc()
	c.grants[code] = grant{rawKey: rawKey, scopes: c.GrantScopes, callback: callback, created: time.Now()}
	c.mu.Unlock()

	if c.Audit != nil {
		c.Audit(r, user.ID, "connect_authorized", map[string]interface{}{"key_id": id, "callback_host": cb.Host, "client": clientLabel})
	}
	logger.Info("connect authorize: approved service=%s user=%d key_id=%d callback_host=%s", c.ID, user.ID, id, cb.Host)
	http.Redirect(w, r, callback+queryJoin(callback)+"code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), http.StatusSeeOther)
}

// Token exchanges a one-time code for the raw key. Server-to-server, no
// session (the code is the credential). CSRF-exempt.
func (c *Service) Token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		c.fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	callback := strings.TrimSpace(r.FormValue("callback"))
	if code == "" {
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<14))
		if readErr != nil {
			logger.Warn("connect token: reading body failed service=%s: %v", c.ID, readErr)
			c.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unreadable body"})
			return
		}
		if len(body) > 0 {
			var in struct {
				Code     string `json:"code"`
				Callback string `json:"callback"`
			}
			if json.Unmarshal(body, &in) == nil {
				code = strings.TrimSpace(in.Code)
				if callback == "" {
					callback = strings.TrimSpace(in.Callback)
				}
			}
		}
	}

	c.mu.Lock()
	c.gc()
	g, found := c.grants[code]
	if found {
		delete(c.grants, code)
	}
	c.mu.Unlock()

	if !found {
		c.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown or expired code"})
		return
	}
	if callback != "" && callback != g.callback {
		logger.Warn("connect token: callback mismatch service=%s got=%q want=%q", c.ID, callback, g.callback)
		c.writeJSON(w, http.StatusBadRequest, map[string]any{"error": "callback mismatch"})
		return
	}

	name := ""
	if c.BusinessName != nil {
		name = strings.TrimSpace(c.BusinessName())
	}
	if name == "" {
		name = c.Label
	}
	c.writeJSON(w, http.StatusOK, map[string]any{
		"key":     g.rawKey,
		"scopes":  g.scopes,
		"base":    c.baseURL(r),
		"service": c.ID,
		"name":    name,
	})
}

const consentCSS = `body{font:16px/1.55 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;max-width:34rem;margin:4rem auto;padding:0 1.25rem;color:#1a1a1a;background:#fafafa}
.card{background:#fff;border:1px solid #e4e4e7;border-radius:14px;padding:1.75rem}
h1{font-size:1.25rem;margin:0 0 .75rem}
.muted{color:#6b7280;font-size:.9rem}
ul{margin:.5rem 0 0;padding-inline-start:1.1rem}
.scopes{background:#f4f4f5;border-radius:10px;padding:.85rem 1rem;margin:1.1rem 0}
button{font:inherit;padding:.65rem 1.2rem;border-radius:9px;border:0;cursor:pointer}
.ok{background:#15803d;color:#fff}.no{background:transparent;color:#6b7280;border:1px solid #d4d4d8;margin-inline-start:.5rem}
`

func (c *Service) Stylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := io.WriteString(w, consentCSS); err != nil {
		logger.Error("connect consent css: write failed service=%s err=%v", c.ID, err)
	}
}

func (c *Service) writeConsentPage(w http.ResponseWriter, r *http.Request, callback, state, clientLabel string, user User) {
	esc := html.EscapeString
	lang := c.Language(r)
	tr := func(key string) string { return esc(c.Translate(lang, key)) }
	scopeLines := ""
	for _, k := range c.ScopeLineKeys {
		scopeLines += "<li>" + tr(k) + "</li>"
	}
	cannot := ""
	if c.ScopesCannotKey != "" {
		cannot = "\n" + tr(c.ScopesCannotKey)
	}
	actor := clientLabel
	if strings.TrimSpace(actor) == "" {
		actor = strings.TrimSpace("your " + strings.TrimSpace(c.ClientName) + " assistant")
		actor = strings.Join(strings.Fields(actor), " ")
	}
	prompt := strings.NewReplacer(
		"{actor}", "<strong>"+esc(actor)+"</strong>",
		"{host}", `<span class="muted">`+esc(c.hostForDisplay(r))+`</span>`,
	).Replace(tr("connect_prompt"))
	note := strings.NewReplacer("{user}", esc(user.Email)).Replace(tr("connect_note"))
	page := `<!doctype html><html lang="` + esc(lang) + `" dir="` + esc(c.LocaleDir(lang)) + `"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + tr("connect_title_tag") + ` ` + esc(clientLabel) + `</title>
<link rel="stylesheet" href="/connect/consent.css"></head><body><div class="card">
<h1>` + tr("connect_heading") + `</h1>
<p>` + prompt + `</p>
<div class="scopes">` + tr("connect_scopes_intro") + `
<ul>` + scopeLines + `</ul>` + cannot + `</div>
<p class="muted">` + note + `</p>
<form method="POST" action="/connect/authorize">
<input type="hidden" name="csrf_token" value="` + esc(c.CSRFToken(r)) + `">
<input type="hidden" name="callback" value="` + esc(callback) + `">
<input type="hidden" name="state" value="` + esc(state) + `">
<input type="hidden" name="bot" value="` + esc(clientLabel) + `">
<button class="ok" name="approve" value="1" type="submit">` + tr("connect_approve") + `</button>
<button class="no" name="approve" value="0" type="submit">` + tr("connect_cancel") + `</button>
</form></div></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := io.WriteString(w, page); err != nil {
		logger.Error("connect consent: write failed service=%s user_id=%d err=%v", c.ID, user.ID, err)
	}
}
