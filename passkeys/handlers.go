package passkeys

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type LoginUser struct {
	ID       int64
	Is2FA    bool
	Disabled bool
	Raw      any
}

type Handlers struct {
	Svc          *Service
	Tag          string
	SettingsURL  string
	ClientIP     func(r *http.Request) string
	LoadUser     func(id int64) (LoginUser, error)
	Start2FA     func(w http.ResponseWriter, r *http.Request, u LoginUser) (string, error)
	IssueSession func(w http.ResponseWriter, r *http.Request, u LoginUser) (dest string, code string, err error)
	CurrentUser  func(r *http.Request) (id int64, email string, ok bool)
	Record       func(r *http.Request, userID int64, action, detail string)
	Flash        func(w http.ResponseWriter, r *http.Request, kind, key string)
}

func writeJSON(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("passkey: write response failed err=%v", err)
	}
}

func (h *Handlers) ip(r *http.Request) string {
	if h.ClientIP == nil {
		return ""
	}
	return h.ClientIP(r)
}

func (h *Handlers) LoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	assertion, token, err := h.Svc.BeginLogin(r)
	if err != nil {
		logger.Error("AUTH passkey_login_begin failed loc=%s err=%v", h.ip(r), err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": assertion, "token": token})
}

func (h *Handlers) LoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method"})
		return
	}
	userID, err := h.Svc.FinishLogin(r, r.URL.Query().Get("t"))
	if err != nil {
		logger.Warn("AUTH passkey_login_failed loc=%s err=%v", h.ip(r), err)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "rejected"})
		return
	}
	u, err := h.LoadUser(userID)
	if err != nil {
		logger.Error("AUTH passkey_login user lookup failed uid=%d err=%v", userID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unavailable"})
		return
	}
	h.Record(r, u.ID, "passkey_login", "")
	if u.Is2FA && !u.Disabled {
		dest, terr := h.Start2FA(w, r, u)
		if terr != nil {
			logger.Error("AUTH passkey_login 2fa token failed uid=%d err=%v", u.ID, terr)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"redirect": dest})
		return
	}
	dest, code, serr := h.IssueSession(w, r, u)
	if serr != nil {
		logger.Error("AUTH passkey_login session failed uid=%d err=%v", u.ID, serr)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unavailable"})
		return
	}
	if code != "" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"redirect": dest})
}

func (h *Handlers) AddBegin(w http.ResponseWriter, r *http.Request) {
	id, email, ok := h.CurrentUser(r)
	if !ok || r.Method != http.MethodPost {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	creation, err := h.Svc.BeginRegistration(r, id, email)
	if err != nil {
		logger.Error("%s passkey_add_begin failed uid=%d err=%v", h.Tag, id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": creation})
}

func (h *Handlers) AddFinish(w http.ResponseWriter, r *http.Request) {
	id, email, ok := h.CurrentUser(r)
	if !ok || r.Method != http.MethodPost {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if err := h.Svc.FinishRegistration(r, id, email, name); err != nil {
		logger.Warn("%s passkey_add_failed uid=%d err=%v", h.Tag, id, err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed"})
		return
	}
	h.Record(r, id, "passkey_added", name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) removeByID(w http.ResponseWriter, r *http.Request, what, failKey, okKey, action string, remove func(userID, id int64) error) {
	userID, _, ok := h.CurrentUser(r)
	if !ok || r.Method != http.MethodPost {
		http.Redirect(w, r, h.SettingsURL, http.StatusSeeOther)
		return
	}
	id, perr := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if perr != nil {
		logger.Warn("%s %s invalid id=%q uid=%d err=%v", h.Tag, what, r.FormValue("id"), userID, perr)
		h.Flash(w, r, "error", failKey)
		http.Redirect(w, r, h.SettingsURL, http.StatusSeeOther)
		return
	}
	if err := remove(userID, id); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			logger.Error("%s %s failed uid=%d id=%d err=%v", h.Tag, what, userID, id, err)
		}
		h.Flash(w, r, "error", failKey)
		http.Redirect(w, r, h.SettingsURL, http.StatusSeeOther)
		return
	}
	h.Record(r, userID, action, strconv.FormatInt(id, 10))
	h.Flash(w, r, "success", okKey)
	http.Redirect(w, r, h.SettingsURL, http.StatusSeeOther)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	h.removeByID(w, r, "passkey_delete", "passkey_remove_failed", "passkey_removed", "passkey_removed", h.Svc.Delete)
}

func (h *Handlers) SessionRevoke(w http.ResponseWriter, r *http.Request) {
	h.removeByID(w, r, "session_revoke", "session_revoke_failed", "session_revoked", "session_revoked", h.Svc.RevokeSession)
}
