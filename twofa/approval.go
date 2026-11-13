package twofa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/CarlSinclair/csuit-libs/telegram"
)

// CallbackPrefix starts the callback data of the approval buttons; a bot hands
// any callback that starts with it to HandleCallback. The data is
// tg2fa:<tenant>:<token>:<y|n>, so a shared bot's hub can find the tenant that
// asked, the same way it does for sign-in buttons.
const CallbackPrefix = "tg2fa:"

const (
	approvalTTLSeconds = 180
	progressTTLSeconds = 600
	approvalKeyPrefix  = "twofa:approval:"
	progressKeyPrefix  = "twofa:done:"
)

var (
	ErrNoRedis = errors.New("twofa: redis unavailable")
)

const decideScript = `
if redis.call('hget', KEYS[1], 'status') ~= 'pending' then return 0 end
if redis.call('hget', KEYS[1], 'chat') ~= ARGV[1] then return -1 end
redis.call('hset', KEYS[1], 'status', ARGV[2])
return 1
`

const pollScript = `
local s = redis.call('hget', KEYS[1], 'secret')
if (not s) or s ~= ARGV[1] then return {'expired', ''} end
local st = redis.call('hget', KEYS[1], 'status')
if st == 'approved' then
  local uid = redis.call('hget', KEYS[1], 'user')
  redis.call('del', KEYS[1])
  return {'approved', uid}
end
if st == 'denied' then
  redis.call('del', KEYS[1])
  return {'denied', ''}
end
return {'pending', ''}
`

// Texts is the wording of the approval message, already in the person's language.
type Texts struct {
	Prompt  string
	Approve string
	Deny    string
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// StartApproval records a pending approval for the person and sends them the
// message with its two buttons. The returned secret is for the page that is
// waiting: only its hash is stored, so a leaked button cannot sign anyone in.
func StartApproval(ctx context.Context, bot *telegram.Bot, tenant string, userID, chatID int64, tx Texts) (token, secret string, err error) {
	if bot == nil {
		return "", "", errors.New("twofa: no Telegram chat to ask")
	}
	return StartApprovalWith(ctx, func(ctx context.Context, chat, text string, buttons []telegram.Button) error {
		_, serr := bot.SendMessageButtons(ctx, chat, text, buttons)
		return serr
	}, tenant, userID, chatID, tx)
}

// StartApprovalWith is StartApproval for callers that deliver the buttons
// through something other than a bot they hold, such as a relay.
func StartApprovalWith(ctx context.Context, send func(ctx context.Context, chat, text string, buttons []telegram.Button) error, tenant string, userID, chatID int64, tx Texts) (token, secret string, err error) {
	if redis.Client() == nil {
		return "", "", ErrNoRedis
	}
	if send == nil || chatID == 0 {
		return "", "", errors.New("twofa: no Telegram chat to ask")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || strings.Contains(tenant, ":") || len(tenant) > 32 {
		return "", "", errors.New("twofa: invalid tenant name")
	}
	if token, err = randHex(10); err != nil {
		return "", "", err
	}
	if secret, err = randHex(16); err != nil {
		return "", "", err
	}
	key := approvalKeyPrefix + token
	pipe := redis.Client().TxPipeline()
	pipe.HSet(redis.Ctx, key, map[string]any{"user": userID, "chat": chatID, "secret": secretHash(secret), "status": "pending"})
	pipe.Expire(redis.Ctx, key, approvalTTLSeconds*1e9)
	if _, err = pipe.Exec(redis.Ctx); err != nil {
		return "", "", fmt.Errorf("twofa: store approval for user %d: %w", userID, err)
	}
	err = send(ctx, strconv.FormatInt(chatID, 10), tx.Prompt, []telegram.Button{
		{Text: tx.Approve, Data: CallbackPrefix + tenant + ":" + token + ":y"},
		{Text: tx.Deny, Data: CallbackPrefix + tenant + ":" + token + ":n"},
	})
	if err != nil {
		if derr := redis.Client().Del(redis.Ctx, key).Err(); derr != nil {
			logger.Error("twofa: removing unsent approval for user %d failed: %v", userID, derr)
		}
		return "", "", fmt.Errorf("twofa: sending approval to chat %d for user %d: %w", chatID, userID, err)
	}
	return token, secret, nil
}

// HandleCallback is what a bot calls with the data of a pressed button. handled
// is false for callbacks that are not approval buttons. approved is the person's
// answer; accepted is false when the approval was already answered, expired or
// pressed from another chat.
func HandleCallback(data string, chatID int64) (handled, approved, accepted bool) {
	if !strings.HasPrefix(data, CallbackPrefix) {
		return false, false, false
	}
	parts := strings.Split(strings.TrimPrefix(data, CallbackPrefix), ":")
	if len(parts) != 3 || len(parts[1]) != 20 || (parts[2] != "y" && parts[2] != "n") {
		return true, false, false
	}
	parts = parts[1:]
	if redis.Client() == nil {
		logger.Error("twofa: approval answer dropped, redis unavailable")
		return true, false, false
	}
	status := "denied"
	if parts[1] == "y" {
		status = "approved"
	}
	res, err := redis.Client().Eval(redis.Ctx, decideScript, []string{approvalKeyPrefix + parts[0]}, strconv.FormatInt(chatID, 10), status).Int()
	if err != nil {
		logger.Error("twofa: recording approval answer from chat %d failed: %v", chatID, err)
		return true, false, false
	}
	if res == -1 {
		logger.Warn("twofa: approval answered from a different chat than it was sent to (chat %d)", chatID)
	}
	return true, parts[1] == "y", res == 1
}

// CallbackTenant returns the tenant named in an approval button's data, for a
// hub that has to pass the press on to the tenant that asked.
func CallbackTenant(data string) (string, bool) {
	if !strings.HasPrefix(data, CallbackPrefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(data, CallbackPrefix), ":")
	if len(parts) != 3 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

// PollApproval is called by the waiting page. An approved answer is returned
// once and then forgotten.
func PollApproval(token, secret string) (status string, userID int64) {
	token = strings.TrimSpace(token)
	if redis.Client() == nil {
		logger.Error("twofa: poll failed, redis unavailable")
		return "expired", 0
	}
	if len(token) != 20 || strings.TrimSpace(secret) == "" {
		return "expired", 0
	}
	res, err := redis.Client().Eval(redis.Ctx, pollScript, []string{approvalKeyPrefix + token}, secretHash(secret)).Slice()
	if err != nil || len(res) != 2 {
		logger.Error("twofa: polling approval failed: %v", err)
		return "expired", 0
	}
	status, _ = res[0].(string)
	if idStr, _ := res[1].(string); idStr != "" && status == "approved" {
		id, perr := strconv.ParseInt(idStr, 10, 64)
		if perr != nil {
			logger.Error("twofa: approval holds a bad user id %q: %v", idStr, perr)
			return "denied", 0
		}
		userID = id
	}
	return status, userID
}

// MarkDone remembers that a method was completed for one sign-in in progress
// (sessionKey is the app's own pending-2FA token). It is how "require all
// methods" carries progress from one step to the next.
func MarkDone(sessionKey string, m Method) error {
	if redis.Client() == nil {
		return ErrNoRedis
	}
	key := progressKeyPrefix + secretHash(sessionKey)
	pipe := redis.Client().TxPipeline()
	pipe.SAdd(redis.Ctx, key, string(m))
	pipe.Expire(redis.Ctx, key, progressTTLSeconds*1e9)
	if _, err := pipe.Exec(redis.Ctx); err != nil {
		return fmt.Errorf("twofa: remember completed method %s: %w", m, err)
	}
	return nil
}

func Done(sessionKey string) (map[Method]bool, error) {
	if redis.Client() == nil {
		return nil, ErrNoRedis
	}
	members, err := redis.Client().SMembers(redis.Ctx, progressKeyPrefix+secretHash(sessionKey)).Result()
	if err != nil {
		return nil, fmt.Errorf("twofa: read completed methods: %w", err)
	}
	out := map[Method]bool{}
	for _, m := range members {
		out[Method(m)] = true
	}
	return out, nil
}

func ClearDone(sessionKey string) {
	if redis.Client() == nil {
		return
	}
	if err := redis.Client().Del(redis.Ctx, progressKeyPrefix+secretHash(sessionKey)).Err(); err != nil {
		logger.Error("twofa: clearing sign-in progress failed: %v", err)
	}
}

const (
	reauthTTLSeconds = 120
	reauthKeyPrefix  = "twofa:reauth:"
)

const takeScript = `
if redis.call('get', KEYS[1]) then redis.call('del', KEYS[1]) return 1 end
return 0
`

func reauthKey(tenant string, userID int64, m Method) string {
	return reauthKeyPrefix + tenant + ":" + strconv.FormatInt(userID, 10) + ":" + string(m)
}

// MarkReauth records that a signed-in user just proved a method for a
// sensitive action (an approval pressed in Telegram, or an emailed code). It
// lasts two minutes and is used up by the first TakeReauth of the same method.
func MarkReauth(tenant string, userID int64, m Method) error {
	if redis.Client() == nil {
		return ErrNoRedis
	}
	if err := redis.Client().Set(redis.Ctx, reauthKey(tenant, userID, m), "1", reauthTTLSeconds*1e9).Err(); err != nil {
		return fmt.Errorf("twofa: remember %s proof for user %d: %w", m, userID, err)
	}
	return nil
}

// TakeReauth reports whether a proof from MarkReauth is waiting, and uses it up.
func TakeReauth(tenant string, userID int64, m Method) (bool, error) {
	if redis.Client() == nil {
		return false, ErrNoRedis
	}
	n, err := redis.Client().Eval(redis.Ctx, takeScript, []string{reauthKey(tenant, userID, m)}).Int()
	if err != nil {
		return false, fmt.Errorf("twofa: read %s proof for user %d: %w", m, userID, err)
	}
	return n == 1, nil
}
