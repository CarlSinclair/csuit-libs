package telegramlogin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const PollSecretHeader = "X-Login-Secret"

type Subject struct {
	ID       int64
	Disabled bool
	TwoFA    bool
	Payload  any
}

type HTTPConfig struct {
	Flow         *Flow
	BotUsername  func() string
	Where        func(r *http.Request) string
	LookupUser   func(id int64) (Subject, error)
	LogSignIn    func(r *http.Request, s Subject)
	Temp2FAToken func(userID int64) (string, error)
	Finalize     func(w http.ResponseWriter, r *http.Request, s Subject)
	TwoFAPath    string
}

func (c HTTPConfig) flow() *Flow {
	if c.Flow != nil {
		return c.Flow
	}
	return defaultFlow
}

func writeJSON(w http.ResponseWriter, status int, v map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("telegram login: write response failed err=%v", err)
	}
}

func (c HTTPConfig) HandleStart(w http.ResponseWriter, r *http.Request) {
	username := c.BotUsername()
	if username == "" {
		logger.Error("AUTH telegram_login_start: platform bot username not configured")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not_configured"})
		return
	}
	secret, err := NewTelegramLoginSecret()
	if err != nil {
		logger.Error("AUTH telegram_login_start: secret failed err=%v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "system_error"})
		return
	}
	token, err := c.flow().CreateTelegramLoginToken(secret, c.Where(r))
	if err != nil {
		logger.Error("AUTH telegram_login_start: token failed err=%v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "system_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token":  token,
		"secret": secret,
		"app":    "tg://resolve?domain=" + username + "&start=" + token,
		"web":    "https://t.me/" + username + "?start=" + token,
	})
}

func (c HTTPConfig) HandlePoll(w http.ResponseWriter, r *http.Request) {
	status, userID := c.flow().PollTelegramLogin(r.URL.Query().Get("t"), r.Header.Get(PollSecretHeader))
	if status != "approved" {
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
		return
	}
	subject, err := c.LookupUser(userID)
	if err != nil {
		logger.Error("AUTH telegram_login: approved user lookup failed uid=%d err=%v", userID, err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
		return
	}
	if subject.Disabled {
		logger.Warn("AUTH telegram_login: approved sign-in refused for disabled account uid=%d", userID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
		return
	}
	c.LogSignIn(r, subject)
	if subject.TwoFA {
		tempToken, err := c.Temp2FAToken(subject.ID)
		if err != nil {
			logger.Error("AUTH telegram_login: 2fa token failed uid=%d err=%v", subject.ID, err)
			writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "mfa_pending", Value: tempToken, Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			Expires: time.Now().Add(5 * time.Minute),
		})
		path := c.TwoFAPath
		if path == "" {
			path = "/auth/2fa"
		}
		http.Redirect(w, r, path, http.StatusSeeOther)
		return
	}
	c.Finalize(w, r, subject)
}
