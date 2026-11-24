package twofa

import (
	"errors"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Step is one sign-in waiting for its second factor. Key is the product's own
// pending-2FA token; progress is kept under it.
type Step[U any] struct {
	User    U
	UserID  int64
	Prefs   Prefs
	Enabled []Method
	Done    map[Method]bool
	Key     string
}

// NewStep reads the progress made so far and, for a sign-in that did not use
// the password, adds the password as an alternative second factor when the
// person has one. "Require every enabled method" never asks for it.
func NewStep[U any](user U, userID int64, key string, prefs Prefs, enabled []Method, hasPassword bool) (*Step[U], error) {
	done, err := Done(key)
	if err != nil {
		return nil, err
	}
	if hasPassword && !prefs.All {
		for m := range done {
			if strings.HasPrefix(string(m), firstPrefix) {
				enabled = append(append([]Method{}, enabled...), MethodPassword)
				break
			}
		}
	}
	return &Step[U]{User: user, UserID: userID, Prefs: prefs, Enabled: enabled, Done: done, Key: key}, nil
}

func (s *Step[U]) Has(m Method) bool {
	for _, e := range s.Enabled {
		if e == m {
			return true
		}
	}
	return false
}

// Open reports whether the method is enabled and not yet completed.
func (s *Step[U]) Open(m Method) bool { return s.Has(m) && !s.Done[m] }

// Finish records a completed method and reports whether the sign-in now has
// enough of them; when it does the progress is cleared.
func (s *Step[U]) Finish(m Method) (complete bool, err error) {
	if err := MarkDone(s.Key, m); err != nil {
		return false, err
	}
	s.Done[m] = true
	if !s.Prefs.Satisfied(s.Enabled, s.Done) {
		return false, nil
	}
	ClearDone(s.Key)
	return true, nil
}

// StepHost carries the sign-in step endpoints that start a Telegram or email
// approval and poll for the answer. Products supply how the pending sign-in is
// found, how Telegram and the email are sent and how the session is issued.
type StepHost[U any] struct {
	Load          func(r *http.Request) (*Step[U], string)
	StartTelegram func(r *http.Request, s *Step[U]) (token, secret string, err error)
	SendEmailLink func(r *http.Request, s *Step[U]) (token, secret string, err error)
	Complete      func(w http.ResponseWriter, r *http.Request, s *Step[U]) (next, errCode string)
	JSON          func(w http.ResponseWriter, r *http.Request, v any)
	Fail          func(w http.ResponseWriter, r *http.Request, status int, key string)
}

func (h StepHost[U]) TelegramStart(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MethodTelegram, h.StartTelegram, "tfa_login_telegram_failed")
}

func (h StepHost[U]) EmailStart(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, MethodEmail, h.SendEmailLink, "tfa_login_email_failed")
}

func (h StepHost[U]) start(w http.ResponseWriter, r *http.Request, m Method, send func(*http.Request, *Step[U]) (string, string, error), failKey string) {
	w.Header().Set("Cache-Control", "no-store")
	s, code := h.Load(r)
	if s == nil {
		h.Fail(w, r, http.StatusUnauthorized, code)
		return
	}
	if !s.Open(m) {
		logger.Warn("AUTH 2fa_step %s start refused, method not open uid=%d", m, s.UserID)
		h.Fail(w, r, http.StatusForbidden, "invalid_request")
		return
	}
	token, secret, err := send(r, s)
	if err != nil {
		if errors.Is(err, ErrEmailCooldown) {
			h.Fail(w, r, http.StatusTooManyRequests, "tfa_login_email_wait")
			return
		}
		logger.Error("AUTH 2fa_step %s start failed uid=%d err=%v", m, s.UserID, err)
		h.Fail(w, r, http.StatusBadGateway, failKey)
		return
	}
	logger.Info("AUTH 2fa_step %s sent uid=%d", m, s.UserID)
	h.JSON(w, r, map[string]any{"token": token, "secret": secret})
}

// Poll answers the waiting page. When the approval is in and it completes the
// sign-in it returns the destination in next; when other methods are still
// needed it returns status "more" and the page reloads.
func (h StepHost[U]) Poll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s, code := h.Load(r)
	if s == nil {
		h.Fail(w, r, http.StatusUnauthorized, code)
		return
	}
	status, uid, m := PollApprovalMethod(r.URL.Query().Get("t"), r.Header.Get("X-Login-Secret"))
	if status != "approved" {
		h.JSON(w, r, map[string]any{"status": status})
		return
	}
	if uid != s.UserID || !s.Has(m) {
		logger.Warn("AUTH 2fa_step approval does not match the sign-in uid=%d approved_uid=%d method=%s", s.UserID, uid, m)
		h.JSON(w, r, map[string]any{"status": "expired"})
		return
	}
	complete, err := s.Finish(m)
	if err != nil {
		logger.Error("AUTH 2fa_step could not record method=%s uid=%d err=%v", m, s.UserID, err)
		h.Fail(w, r, http.StatusInternalServerError, "system_error")
		return
	}
	if !complete {
		h.JSON(w, r, map[string]any{"status": "more"})
		return
	}
	next, errCode := h.Complete(w, r, s)
	if errCode != "" {
		h.Fail(w, r, http.StatusForbidden, errCode)
		return
	}
	logger.Info("AUTH 2fa_ok uid=%d method=%s", s.UserID, m)
	h.JSON(w, r, map[string]any{"status": "approved", "next": next})
}
