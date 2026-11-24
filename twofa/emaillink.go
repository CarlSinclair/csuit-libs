package twofa

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

const (
	emailLinkTTLSeconds  = 600
	emailCooldownSeconds = 30
	emailCooldownPrefix  = "twofa:emailcd:"
	emailChat            = "email"
)

var ErrEmailCooldown = errors.New("twofa: an email link was sent a moment ago")

const emailPendingScript = `
if redis.call('hget', KEYS[1], 'chat') ~= 'email' then return 0 end
if redis.call('hget', KEYS[1], 'status') ~= 'pending' then return 0 end
return 1
`

func emailCooldownKey(tenant string, userID int64) string {
	return emailCooldownPrefix + tenant + ":" + strconv.FormatInt(userID, 10)
}

// StartEmailApproval records a pending email approval for the person. The
// caller emails the link (it carries token) and hands token and secret to the
// waiting page, which polls with PollApprovalMethod exactly as for Telegram.
// Asking again within thirty seconds returns ErrEmailCooldown. When sending the
// email fails the caller calls DiscardEmailApproval so the person can retry.
func StartEmailApproval(tenant string, userID int64) (token, secret string, err error) {
	if redis.Client() == nil {
		return "", "", ErrNoRedis
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || strings.Contains(tenant, ":") || len(tenant) > 32 {
		return "", "", errors.New("twofa: invalid tenant name")
	}
	ok, err := redis.Client().SetNX(redis.Ctx, emailCooldownKey(tenant, userID), "1", emailCooldownSeconds*1e9).Result()
	if err != nil {
		return "", "", fmt.Errorf("twofa: email link cooldown for user %d: %w", userID, err)
	}
	if !ok {
		return "", "", ErrEmailCooldown
	}
	if token, err = randHex(10); err != nil {
		DiscardEmailApproval(tenant, userID, "")
		return "", "", err
	}
	if secret, err = randHex(16); err != nil {
		DiscardEmailApproval(tenant, userID, "")
		return "", "", err
	}
	key := approvalKeyPrefix + token
	pipe := redis.Client().TxPipeline()
	pipe.HSet(redis.Ctx, key, map[string]any{"user": userID, "chat": emailChat, "secret": secretHash(secret), "status": "pending"})
	pipe.Expire(redis.Ctx, key, emailLinkTTLSeconds*1e9)
	if _, err = pipe.Exec(redis.Ctx); err != nil {
		DiscardEmailApproval(tenant, userID, "")
		return "", "", fmt.Errorf("twofa: store email approval for user %d: %w", userID, err)
	}
	return token, secret, nil
}

// DiscardEmailApproval forgets an approval whose email could not be delivered.
func DiscardEmailApproval(tenant string, userID int64, token string) {
	if redis.Client() == nil {
		return
	}
	keys := []string{emailCooldownKey(tenant, userID)}
	if token != "" {
		keys = append(keys, approvalKeyPrefix+token)
	}
	if err := redis.Client().Del(redis.Ctx, keys...).Err(); err != nil {
		logger.Error("twofa: removing undelivered email approval for user %d failed: %v", userID, err)
	}
}

// EmailLinkPending reports whether the link's token still waits for a click.
func EmailLinkPending(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) != 20 || redis.Client() == nil {
		return false
	}
	n, err := redis.Client().Eval(redis.Ctx, emailPendingScript, []string{approvalKeyPrefix + token}).Int()
	if err != nil {
		logger.Error("twofa: reading email approval failed: %v", err)
		return false
	}
	return n == 1
}

// ApproveEmailLink marks the email approval as approved. It is called only from
// a POST, never from the link's GET, so a mail scanner that opens the link
// cannot approve a sign-in.
func ApproveEmailLink(token string) bool {
	token = strings.TrimSpace(token)
	if len(token) != 20 || redis.Client() == nil {
		return false
	}
	res, err := redis.Client().Eval(redis.Ctx, decideScript, []string{approvalKeyPrefix + token}, emailChat, "approved").Int()
	if err != nil {
		logger.Error("twofa: recording email approval failed: %v", err)
		return false
	}
	return res == 1
}

// Link states handed to LinkHost.Page.
const (
	LinkConfirm = "confirm"
	LinkDone    = "done"
	LinkInvalid = "invalid"
)

// LinkHost serves the page behind the emailed link: GET shows a button, POST
// approves. Page renders each state in the product's own layout.
type LinkHost struct {
	Page func(w http.ResponseWriter, r *http.Request, state, token string)
}

func (h LinkHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token := strings.TrimSpace(r.FormValue("t"))
	if r.Method == http.MethodPost {
		if !ApproveEmailLink(token) {
			logger.Warn("AUTH 2fa_email link not accepted (expired, used or unknown)")
			h.Page(w, r, LinkInvalid, token)
			return
		}
		logger.Info("AUTH 2fa_email link approved")
		h.Page(w, r, LinkDone, token)
		return
	}
	if !EmailLinkPending(token) {
		h.Page(w, r, LinkInvalid, token)
		return
	}
	h.Page(w, r, LinkConfirm, token)
}
