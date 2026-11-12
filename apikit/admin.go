package apikit

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	defaultMaxKeys  = 20
	defaultMaxHooks = 10
	maxLabelRunes   = 100
	maxExpiryDays   = 730
)

type Admin struct {
	Keys          Store
	Hooks         WebhookStore
	Emitter       *Emitter
	AllowedScopes func() []string
	Events        []string
	TokenPrefix   string
	Owner         func(r *http.Request) int64
	Actor         func(r *http.Request) int64
	Audit         func(r *http.Request, action, detail string)
	Seal          func(string) (string, error)
	BaseURL       func(r *http.Request) string
	MaxKeys       int
	MaxHooks      int
}

type KeyView struct {
	ID         int64      `json:"id"`
	Label      string     `json:"label"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastUsedIP string     `json:"last_used_ip"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Revoked    bool       `json:"revoked"`
	Expired    bool       `json:"expired"`
}

type HookView struct {
	ID        int64     `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	FailCount int       `json:"fail_count"`
	CreatedAt time.Time `json:"created_at"`
}

type DeliveryView struct {
	ID         int64     `json:"id"`
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Attempt    int       `json:"attempt"`
	StatusCode int       `json:"status_code"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error"`
	DurationMS int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

type OverviewView struct {
	Keys     []KeyView  `json:"keys"`
	Webhooks []HookView `json:"webhooks"`
	Scopes   []string   `json:"scopes"`
	Presets  []Preset   `json:"presets"`
	Events   []string   `json:"events"`
	BaseURL  string     `json:"base_url"`
}

func keyView(k Key, now time.Time) KeyView {
	return KeyView{
		ID: k.ID, Label: k.Label, Prefix: k.Prefix, Scopes: k.Scopes, CreatedAt: k.CreatedAt,
		LastUsedAt: k.LastUsedAt, LastUsedIP: k.LastUsedIP, ExpiresAt: k.ExpiresAt,
		Revoked: k.Revoked(), Expired: k.Expired(now),
	}
}

func hookView(h Webhook) HookView {
	return HookView{ID: h.ID, URL: h.URL, Events: h.Events, Enabled: h.Enabled, FailCount: h.FailCount, CreatedAt: h.CreatedAt}
}

func (a *Admin) audit(r *http.Request, action, detail string) {
	if a.Audit != nil {
		a.Audit(r, action, detail)
	}
}

func (a *Admin) maxKeys() int {
	if a.MaxKeys > 0 {
		return a.MaxKeys
	}
	return defaultMaxKeys
}

func (a *Admin) maxHooks() int {
	if a.MaxHooks > 0 {
		return a.MaxHooks
	}
	return defaultMaxHooks
}

func (a *Admin) Overview(w http.ResponseWriter, r *http.Request) {
	owner := a.Owner(r)
	keys, err := a.Keys.List(r.Context(), owner)
	if err != nil {
		WriteInternal(w, "list api keys", err)
		return
	}
	hooks, err := a.Hooks.List(r.Context(), owner)
	if err != nil {
		WriteInternal(w, "list api webhooks", err)
		return
	}
	now := time.Now()
	out := OverviewView{Keys: []KeyView{}, Webhooks: []HookView{}, Scopes: a.AllowedScopes(), Presets: PresetsFor(a.AllowedScopes()), Events: a.Events}
	for _, k := range keys {
		out.Keys = append(out.Keys, keyView(k, now))
	}
	for _, h := range hooks {
		out.Webhooks = append(out.Webhooks, hookView(h))
	}
	if a.BaseURL != nil {
		out.BaseURL = a.BaseURL(r)
	}
	WriteJSON(w, http.StatusOK, out)
}

type createKeyRequest struct {
	Label       string   `json:"label"`
	Scopes      []string `json:"scopes"`
	ExpiresDays int      `json:"expires_days"`
}

type createKeyResponse struct {
	Key   KeyView `json:"key"`
	Token string  `json:"token"`
}

func (a *Admin) CreateKey(w http.ResponseWriter, r *http.Request) {
	var req createKeyRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || utf8.RuneCountInString(label) > maxLabelRunes {
		WriteError(w, http.StatusBadRequest, "invalid_label", "label is required and must be at most 100 characters")
		return
	}
	scopes, err := NormalizeScopes(req.Scopes, a.AllowedScopes())
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_scopes", err.Error())
		return
	}
	if req.ExpiresDays < 0 || req.ExpiresDays > maxExpiryDays {
		WriteError(w, http.StatusBadRequest, "invalid_expiry", "expiry must be between 0 and 730 days")
		return
	}
	owner := a.Owner(r)
	existing, err := a.Keys.List(r.Context(), owner)
	if err != nil {
		WriteInternal(w, "count api keys", err)
		return
	}
	active := 0
	now := time.Now()
	for _, k := range existing {
		if !k.Revoked() && !k.Expired(now) {
			active++
		}
	}
	if active >= a.maxKeys() {
		WriteError(w, http.StatusConflict, "key_limit", "the maximum number of active keys was reached")
		return
	}
	raw, hash, display, err := NewToken(a.TokenPrefix)
	if err != nil {
		WriteInternal(w, "generate api key", err)
		return
	}
	k := Key{Label: label, Prefix: display, Scopes: scopes, CreatedBy: a.Actor(r)}
	if owner != 0 {
		k.CreatedBy = owner
	}
	if req.ExpiresDays > 0 {
		t := now.Add(time.Duration(req.ExpiresDays) * 24 * time.Hour)
		k.ExpiresAt = &t
	}
	id, err := a.Keys.Insert(r.Context(), k, hash)
	if err != nil {
		WriteInternal(w, "store api key", err)
		return
	}
	k.ID = id
	k.CreatedAt = now.UTC()
	a.audit(r, "api_key_created", "id="+strconv.FormatInt(id, 10)+" label="+label)
	WriteJSON(w, http.StatusCreated, createKeyResponse{Key: keyView(k, now), Token: raw})
}

type idRequest struct {
	ID int64 `json:"id"`
}

func (a *Admin) RevokeKey(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	ok, err := a.Keys.Revoke(r.Context(), req.ID, a.Owner(r), time.Now())
	if err != nil {
		WriteInternal(w, "revoke api key", err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "key not found or already revoked")
		return
	}
	a.audit(r, "api_key_revoked", "id="+strconv.FormatInt(req.ID, 10))
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type createHookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

type createHookResponse struct {
	Webhook HookView `json:"webhook"`
	Secret  string   `json:"secret"`
}

func (a *Admin) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req createHookRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := ValidateWebhookURL(req.URL); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_url", "the URL must be a public https address")
		return
	}
	events, err := NormalizeScopes(req.Events, append([]string{"*"}, a.Events...))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_events", err.Error())
		return
	}
	owner := a.Owner(r)
	existing, err := a.Hooks.List(r.Context(), owner)
	if err != nil {
		WriteInternal(w, "count api webhooks", err)
		return
	}
	if len(existing) >= a.maxHooks() {
		WriteError(w, http.StatusConflict, "webhook_limit", "the maximum number of webhooks was reached")
		return
	}
	secret, err := NewSecret()
	if err != nil {
		WriteInternal(w, "generate webhook secret", err)
		return
	}
	sealed, err := a.Seal(secret)
	if err != nil {
		WriteInternal(w, "seal webhook secret", err)
		return
	}
	hook := Webhook{OwnerID: owner, URL: strings.TrimSpace(req.URL), SecretEnc: sealed, Events: events, Enabled: true}
	id, err := a.Hooks.Create(r.Context(), hook)
	if err != nil {
		WriteInternal(w, "store api webhook", err)
		return
	}
	hook.ID = id
	hook.CreatedAt = time.Now().UTC()
	a.audit(r, "api_webhook_created", "id="+strconv.FormatInt(id, 10)+" url="+hook.URL)
	WriteJSON(w, http.StatusCreated, createHookResponse{Webhook: hookView(hook), Secret: secret})
}

func (a *Admin) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	ok, err := a.Hooks.Delete(r.Context(), req.ID, a.Owner(r))
	if err != nil {
		WriteInternal(w, "delete api webhook", err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "webhook not found")
		return
	}
	a.audit(r, "api_webhook_deleted", "id="+strconv.FormatInt(req.ID, 10))
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type toggleRequest struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
}

func (a *Admin) ToggleWebhook(w http.ResponseWriter, r *http.Request) {
	var req toggleRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	ok, err := a.Hooks.SetEnabled(r.Context(), req.ID, a.Owner(r), req.Enabled)
	if err != nil {
		WriteInternal(w, "toggle api webhook", err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "webhook not found")
		return
	}
	a.audit(r, "api_webhook_toggled", "id="+strconv.FormatInt(req.ID, 10)+" enabled="+strconv.FormatBool(req.Enabled))
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Admin) owned(w http.ResponseWriter, r *http.Request, id int64) (*Webhook, bool) {
	hook, err := a.Hooks.Get(r.Context(), id)
	if err != nil {
		WriteInternal(w, "load api webhook", err)
		return nil, false
	}
	owner := a.Owner(r)
	if hook == nil || (owner != 0 && hook.OwnerID != owner) {
		WriteError(w, http.StatusNotFound, "not_found", "webhook not found")
		return nil, false
	}
	return hook, true
}

func (a *Admin) TestWebhook(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	hook, ok := a.owned(w, r, req.ID)
	if !ok {
		return
	}
	d, err := a.Emitter.SendTest(r.Context(), hook)
	if err != nil {
		WriteInternal(w, "send webhook test", err)
		return
	}
	logger.Info("apikit: webhook test sent webhook_id=%d ok=%t status=%d", hook.ID, d.OK, d.StatusCode)
	a.audit(r, "api_webhook_tested", "id="+strconv.FormatInt(hook.ID, 10))
	WriteJSON(w, http.StatusOK, deliveryView(d))
}

func deliveryView(d Delivery) DeliveryView {
	return DeliveryView{
		ID: d.ID, EventID: d.EventID, EventType: d.EventType, Attempt: d.Attempt, StatusCode: d.StatusCode,
		OK: d.OK, Error: d.Error, DurationMS: d.DurationMS, CreatedAt: d.CreatedAt,
	}
}

func (a *Admin) Deliveries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("webhook_id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "invalid_id", "webhook_id is required")
		return
	}
	list, err := a.Hooks.Deliveries(r.Context(), id, a.Owner(r), 50)
	if err != nil {
		WriteInternal(w, "list webhook deliveries", err)
		return
	}
	out := make([]DeliveryView, 0, len(list))
	for _, d := range list {
		out = append(out, deliveryView(d))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}
