package telegramlogin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

// Sign-in through the platform's Telegram bot. The browser asks for a login
// token and opens the bot with it (tg://resolve?domain=<bot>&start=<token>),
// so the Telegram app itself opens and no domain has to be registered with
// BotFather. The controller relays the /start to the tenant named inside the
// token, the bot asks the person to confirm, and the page that asked is signed
// in when it polls with the secret it was given at the start.
//
// A token is login_<tenant>_<16 hex>. The tenant is the exact directory name
// (a-z, 0-9 and "-", at most 32 characters, so it can never contain "_"), which
// lets the controller go straight to the owning tenant. The state lives in
// Redis for five minutes under the tenant's own key prefix. The poll secret is
// never sent to Telegram: only its hash is stored, so a token that leaks from a
// shared link or screen cannot be used to sign in elsewhere. No cookie is used.
//
// Account-link tokens (CreateTelegramAdminLinkToken) carry the tenant name the
// same way: link_<tenant>_<16 characters>.

type Config struct {
	OwnDirName func() string
	FindUsers  func(chatID int64) ([]int64, error)
	Text       func(langHint, key string) string
}

var cfg Config

func Configure(c Config) { cfg = c }

func text(langHint, key string) string {
	if cfg.Text == nil {
		return key
	}
	return cfg.Text(langHint, key)
}

const (
	TelegramLoginPrefix = "login_"
	TelegramLinkPrefix  = "link_"
	telegramLoginTTL    = 5 * time.Minute
)

var (
	telegramLoginTokenRE = regexp.MustCompile(`^login_([a-z0-9][a-z0-9-]{2,31})_([0-9a-f]{16})$`)
	telegramLinkTokenRE  = regexp.MustCompile(`^link_([a-z0-9][a-z0-9-]{2,31})_([A-Za-z0-9_-]{16})$`)
)

func telegramLoginKey(token string) string { return redis.Key("tg_login:" + token) }

func telegramLoginHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IsTelegramLoginToken reports whether a /start payload is a sign-in token.
func IsTelegramLoginToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), TelegramLoginPrefix)
}

// ParseTelegramLoginToken returns the tenant a sign-in token was issued by.
func ParseTelegramLoginToken(token string) (tenantDir string, ok bool) {
	m := telegramLoginTokenRE.FindStringSubmatch(strings.TrimSpace(token))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// TelegramTokenTenant reports which tenant issued a /start token. named is true
// for sign-in and account-link tokens; dir is empty when such a token is
// malformed. A bare /start or any other payload is not named.
func TelegramTokenTenant(token string) (dir string, named bool) {
	token = strings.TrimSpace(token)
	switch {
	case strings.HasPrefix(token, TelegramLoginPrefix):
		dir, _ = ParseTelegramLoginToken(token)
		return dir, true
	case strings.HasPrefix(token, TelegramLinkPrefix):
		if m := telegramLinkTokenRE.FindStringSubmatch(token); m != nil {
			return m[1], true
		}
		return "", true
	}
	return "", false
}

// NewTelegramLoginSecret returns the page-held secret that polls must present.
func NewTelegramLoginSecret() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateTelegramLoginToken issues a sign-in token bound to secret. where is
// shown to the person in the confirmation message.
func CreateTelegramLoginToken(secret, where string) (string, error) {
	if redis.Client() == nil {
		return "", fmt.Errorf("redis unavailable")
	}
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("missing secret")
	}
	dir := strings.TrimSpace(cfg.OwnDirName())
	if dir == "" {
		return "", fmt.Errorf("tenant name unavailable")
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		logger.Error("CreateTelegramLoginToken: rand.Read failed: %v", err)
		return "", err
	}
	token := TelegramLoginPrefix + dir + "_" + hex.EncodeToString(b)
	if _, ok := ParseTelegramLoginToken(token); !ok {
		return "", fmt.Errorf("tenant name %q cannot be used in a login token", dir)
	}

	key := telegramLoginKey(token)
	pipe := redis.Client().TxPipeline()
	pipe.HSet(redis.Ctx, key, "secret", telegramLoginHash(secret), "where", where, "stage", "pending")
	pipe.Expire(redis.Ctx, key, telegramLoginTTL)
	if _, err := pipe.Exec(redis.Ctx); err != nil {
		logger.Error("CreateTelegramLoginToken: redis write failed: %v", err)
		return "", err
	}
	return token, nil
}

const (
	loginStartScript = `
if redis.call('HGET', KEYS[1], 'stage') ~= 'pending' then return 0 end
redis.call('HSET', KEYS[1], 'stage', 'awaiting', 'user_id', ARGV[1], 'chat_id', ARGV[2])
return 1`
	loginRejectScript = `
if redis.call('HGET', KEYS[1], 'stage') ~= 'pending' then return 0 end
redis.call('HSET', KEYS[1], 'stage', 'rejected')
return 1`
	loginConfirmScript = `
if redis.call('HGET', KEYS[1], 'stage') == 'awaiting' and redis.call('HGET', KEYS[1], 'chat_id') == ARGV[1] then
  redis.call('HSET', KEYS[1], 'stage', 'approved')
  return 1
end
return 0`
	loginPollScript = `
local h = redis.call('HGET', KEYS[1], 'secret')
if not h or h ~= ARGV[1] then return {'expired', '0'} end
local st = redis.call('HGET', KEYS[1], 'stage')
if st == 'approved' then
  local u = redis.call('HGET', KEYS[1], 'user_id')
  redis.call('DEL', KEYS[1])
  return {'approved', u}
end
if st == 'rejected' then
  redis.call('DEL', KEYS[1])
  return {'rejected', '0'}
end
return {st, '0'}`
)

func loginEvalInt(script, key string, args ...interface{}) (int64, error) {
	return redis.Client().Eval(redis.Ctx, script, []string{key}, args...).Int64()
}

// ProcessTelegramLoginStart handles the bot's /start <token>. owned is false
// when this tenant has no such pending sign-in. confirm and confirmLabel are
// set when the person must confirm with the button; reply is the message to
// send either way. Only an enabled admin or staff account linked to this
// Telegram account can sign in.
func ProcessTelegramLoginStart(token string, chatID int64, chatType, langHint string) (owned bool, reply, confirm, confirmLabel string) {
	token = strings.TrimSpace(token)
	invalid := "❌ " + text(langHint, "tg_link_invalid")
	sysErr := "❌ " + text(langHint, "tg_sys_err")

	if redis.Client() == nil {
		logger.Error("ProcessTelegramLoginStart: redis unavailable")
		return true, sysErr, "", ""
	}
	if _, ok := ParseTelegramLoginToken(token); !ok {
		return false, invalid, "", ""
	}
	key := telegramLoginKey(token)
	fields, err := redis.Client().HMGet(redis.Ctx, key, "stage", "where").Result()
	if err != nil {
		logger.Error("ProcessTelegramLoginStart: redis read failed: %v", err)
		return true, sysErr, "", ""
	}
	stage, _ := fields[0].(string)
	where, _ := fields[1].(string)
	if stage == "" {
		return false, invalid, "", ""
	}
	if stage != "pending" {
		return true, invalid, "", ""
	}

	reject := func(msgKey string) (bool, string, string, string) {
		if _, err := loginEvalInt(loginRejectScript, key); err != nil {
			logger.Error("ProcessTelegramLoginStart: could not mark sign-in rejected: %v", err)
		}
		return true, "❌ " + text(langHint, msgKey), "", ""
	}
	if strings.ToLower(strings.TrimSpace(chatType)) != "private" || chatID <= 0 {
		return reject("tg_login_private_only")
	}
	if cfg.FindUsers == nil {
		logger.Error("ProcessTelegramLoginStart: user lookup not configured")
		return reject("tg_sys_err")
	}
	ids, err := cfg.FindUsers(chatID)
	if err != nil {
		logger.Error("ProcessTelegramLoginStart: user lookup failed chat_id=%d err=%v", chatID, err)
		return reject("tg_sys_err")
	}
	if len(ids) != 1 {
		logger.Warn("ProcessTelegramLoginStart: chat_id=%d matches %d users, refusing", chatID, len(ids))
		return reject("tg_login_not_linked")
	}

	started, err := loginEvalInt(loginStartScript, key, ids[0], chatID)
	if err != nil {
		logger.Error("ProcessTelegramLoginStart: redis update failed: %v", err)
		return true, sysErr, "", ""
	}
	if started != 1 {
		return true, invalid, "", ""
	}
	reply = "🔐 " + text(langHint, "tg_login_ask")
	if strings.TrimSpace(where) != "" {
		reply += "\n\n🌍 " + where
	}
	return true, reply, token, "✅ " + text(langHint, "tg_login_confirm_btn")
}

// ProcessTelegramLoginConfirm handles the confirm button. Only the chat the
// start came from can approve it.
func ProcessTelegramLoginConfirm(token string, chatID int64, langHint string) (owned bool, reply string) {
	token = strings.TrimSpace(token)
	if redis.Client() == nil {
		logger.Error("ProcessTelegramLoginConfirm: redis unavailable")
		return true, "❌ " + text(langHint, "tg_sys_err")
	}
	if _, ok := ParseTelegramLoginToken(token); !ok {
		return false, ""
	}
	key := telegramLoginKey(token)
	exists, err := redis.Client().Exists(redis.Ctx, key).Result()
	if err != nil {
		logger.Error("ProcessTelegramLoginConfirm: redis read failed: %v", err)
		return true, "❌ " + text(langHint, "tg_sys_err")
	}
	if exists == 0 {
		return false, ""
	}
	approved, err := loginEvalInt(loginConfirmScript, key, fmt.Sprintf("%d", chatID))
	if err != nil {
		logger.Error("ProcessTelegramLoginConfirm: redis update failed: %v", err)
		return true, "❌ " + text(langHint, "tg_sys_err")
	}
	if approved != 1 {
		return true, "❌ " + text(langHint, "tg_link_invalid")
	}
	return true, "✅ " + text(langHint, "tg_login_done")
}

// PollTelegramLogin reports where a sign-in stands for the page that holds the
// secret: pending, awaiting (the person has been asked to confirm), approved
// (the token is consumed and userID is who to sign in), rejected or expired.
func PollTelegramLogin(token, secret string) (status string, userID int64) {
	token = strings.TrimSpace(token)
	if redis.Client() == nil {
		logger.Error("PollTelegramLogin: redis unavailable")
		return "expired", 0
	}
	if _, ok := ParseTelegramLoginToken(token); !ok || strings.TrimSpace(secret) == "" {
		return "expired", 0
	}
	res, err := redis.Client().Eval(redis.Ctx, loginPollScript, []string{telegramLoginKey(token)}, telegramLoginHash(secret)).Slice()
	if err != nil || len(res) != 2 {
		logger.Error("PollTelegramLogin: redis eval failed: %v", err)
		return "expired", 0
	}
	status, _ = res[0].(string)
	if idStr, _ := res[1].(string); idStr != "" && status == "approved" {
		if _, err := fmt.Sscanf(idStr, "%d", &userID); err != nil {
			logger.Error("PollTelegramLogin: bad user id %q: %v", idStr, err)
			return "rejected", 0
		}
	}
	return status, userID
}
