package twofa

import (
	"errors"
	"net/http"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// ConfirmHost carries the four endpoints a signed-in person's confirm dialog
// calls (config, Telegram start, email send and poll). Each product
// supplies how it finds the person, reads their methods and sends and checks
// codes; the endpoints themselves, their logging and their proof recording
// live here once.
type ConfirmHost[U any] struct {
	Resolve       func(w http.ResponseWriter, r *http.Request) (U, bool)
	UserID        func(u U) int64
	Methods       func(u U) (all, telegram, email bool, err error)
	Texts         func(r *http.Request, u U) map[string]string
	StartTelegram func(r *http.Request, u U) (token, secret string, err error)
	SendEmail     func(r *http.Request, u U) (token, secret string, err error)
	Tenant        func() string
	Allow         func(w http.ResponseWriter, r *http.Request, u U, bucket string) bool
	JSON          func(w http.ResponseWriter, r *http.Request, v any)
	Fail          func(w http.ResponseWriter, r *http.Request, status int, key string)
}

func (h ConfirmHost[U]) allowed(w http.ResponseWriter, r *http.Request, u U, bucket string) bool {
	return h.Allow == nil || h.Allow(w, r, u, bucket)
}

func (h ConfirmHost[U]) methods(w http.ResponseWriter, r *http.Request, u U) (all, telegram, email, ok bool) {
	all, telegram, email, err := h.Methods(u)
	if err != nil {
		logger.Error("AUTH 2fa_confirm methods lookup failed uid=%d err=%v", h.UserID(u), err)
		h.Fail(w, r, http.StatusInternalServerError, "internal_error")
		return false, false, false, false
	}
	return all, telegram, email, true
}

func (h ConfirmHost[U]) Config(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := h.Resolve(w, r)
	if !ok {
		return
	}
	all, telegram, email, ok := h.methods(w, r, u)
	if !ok {
		return
	}
	h.JSON(w, r, map[string]any{"telegram": telegram, "email": email, "all": all, "t": h.Texts(r, u)})
}

func (h ConfirmHost[U]) Start(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := h.Resolve(w, r)
	if !ok {
		return
	}
	_, telegram, _, ok := h.methods(w, r, u)
	if !ok {
		return
	}
	if !telegram {
		logger.Warn("AUTH 2fa_confirm telegram start refused, method not enabled uid=%d", h.UserID(u))
		h.Fail(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if !h.allowed(w, r, u, "tg") {
		return
	}
	token, secret, err := h.StartTelegram(r, u)
	if err != nil {
		logger.Error("AUTH 2fa_confirm start failed uid=%d err=%v", h.UserID(u), err)
		h.Fail(w, r, http.StatusBadGateway, "tfa_login_telegram_failed")
		return
	}
	logger.Info("AUTH 2fa_confirm sent uid=%d", h.UserID(u))
	h.JSON(w, r, map[string]any{"token": token, "secret": secret})
}

func (h ConfirmHost[U]) Poll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := h.Resolve(w, r)
	if !ok {
		return
	}
	uid := h.UserID(u)
	status, approvedUID, method := PollApprovalMethod(r.URL.Query().Get("t"), r.Header.Get("X-Login-Secret"))
	if status != "approved" {
		h.JSON(w, r, map[string]any{"status": status})
		return
	}
	if approvedUID != uid {
		logger.Warn("AUTH 2fa_confirm approval for another account uid=%d approved_uid=%d", uid, approvedUID)
		h.JSON(w, r, map[string]any{"status": "expired"})
		return
	}
	if err := MarkReauth(h.Tenant(), uid, method); err != nil {
		logger.Error("AUTH 2fa_confirm could not record approval uid=%d method=%s err=%v", uid, method, err)
		h.Fail(w, r, http.StatusInternalServerError, "tfa_login_telegram_failed")
		return
	}
	h.JSON(w, r, map[string]any{"status": "approved"})
}

func (h ConfirmHost[U]) EmailSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u, ok := h.Resolve(w, r)
	if !ok {
		return
	}
	_, _, email, ok := h.methods(w, r, u)
	if !ok {
		return
	}
	if !email {
		logger.Warn("AUTH 2fa_confirm email send refused, method not enabled uid=%d", h.UserID(u))
		h.Fail(w, r, http.StatusBadRequest, "bad_request")
		return
	}
	if !h.allowed(w, r, u, "mail") {
		return
	}
	token, secret, err := h.SendEmail(r, u)
	if err != nil {
		if errors.Is(err, ErrEmailCooldown) {
			h.Fail(w, r, http.StatusTooManyRequests, "tfa_login_email_wait")
			return
		}
		logger.Error("AUTH 2fa_confirm email send failed uid=%d err=%v", h.UserID(u), err)
		h.Fail(w, r, http.StatusBadGateway, "tfa_login_email_failed")
		return
	}
	logger.Info("AUTH 2fa_confirm email sent uid=%d", h.UserID(u))
	h.JSON(w, r, map[string]any{"token": token, "secret": secret})
}

func ConfirmFlags(p Prefs, enabled []Method) (all, telegram, email bool) {
	for _, m := range enabled {
		switch m {
		case MethodTelegram:
			telegram = true
		case MethodEmail:
			email = true
		}
	}
	return p.All, telegram, email
}
