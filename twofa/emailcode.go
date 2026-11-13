package twofa

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/redis"
)

const (
	emailCodeTTLSeconds  = 600
	emailCooldownSeconds = 30
	emailMaxTries        = 5
	emailKeyPrefix       = "twofa:email:"
	emailCooldownPrefix  = "twofa:emailcd:"
)

var ErrEmailCooldown = errors.New("twofa: an email code was sent a moment ago")

const emailCheckScript = `
local h = redis.call('hget', KEYS[1], 'h')
if not h then return 0 end
local tries = redis.call('hincrby', KEYS[1], 'tries', 1)
if tries > tonumber(ARGV[2]) then redis.call('del', KEYS[1]) return 0 end
if h == ARGV[1] then redis.call('del', KEYS[1]) return 1 end
return 0
`

func emailKey(tenant string, userID int64) string {
	return emailKeyPrefix + tenant + ":" + strconv.FormatInt(userID, 10)
}

func emailCooldownKey(tenant string, userID int64) string {
	return emailCooldownPrefix + tenant + ":" + strconv.FormatInt(userID, 10)
}

func emailCodeHash(tenant string, userID int64, code string) string {
	sum := sha256.Sum256([]byte(tenant + ":" + strconv.FormatInt(userID, 10) + ":" + code))
	return hex.EncodeToString(sum[:])
}

func normalizeEmailCode(code string) string {
	return strings.ReplaceAll(strings.TrimSpace(code), " ", "")
}

// NewEmailCode makes a six digit code for the person and remembers only its
// hash for ten minutes. A new code replaces the old one; asking again within
// thirty seconds returns ErrEmailCooldown. The caller emails the code and, when
// that fails, calls DiscardEmailCode so the person can ask again at once.
func NewEmailCode(tenant string, userID int64) (string, error) {
	if redis.Client() == nil {
		return "", ErrNoRedis
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" || strings.Contains(tenant, ":") || len(tenant) > 32 {
		return "", errors.New("twofa: invalid tenant name")
	}
	ok, err := redis.Client().SetNX(redis.Ctx, emailCooldownKey(tenant, userID), "1", emailCooldownSeconds*1e9).Result()
	if err != nil {
		return "", fmt.Errorf("twofa: email code cooldown for user %d: %w", userID, err)
	}
	if !ok {
		return "", ErrEmailCooldown
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		DiscardEmailCode(tenant, userID)
		return "", err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	key := emailKey(tenant, userID)
	pipe := redis.Client().TxPipeline()
	pipe.Del(redis.Ctx, key)
	pipe.HSet(redis.Ctx, key, map[string]any{"h": emailCodeHash(tenant, userID, code), "tries": 0})
	pipe.Expire(redis.Ctx, key, emailCodeTTLSeconds*1e9)
	if _, err := pipe.Exec(redis.Ctx); err != nil {
		DiscardEmailCode(tenant, userID)
		return "", fmt.Errorf("twofa: store email code for user %d: %w", userID, err)
	}
	return code, nil
}

// DiscardEmailCode forgets a code that could not be delivered.
func DiscardEmailCode(tenant string, userID int64) {
	if redis.Client() == nil {
		return
	}
	redis.Client().Del(redis.Ctx, emailKey(tenant, userID), emailCooldownKey(tenant, userID))
}

// CheckEmailCode reports whether the code matches the one last sent, and uses
// it up when it does. After five wrong tries the code is gone.
func CheckEmailCode(tenant string, userID int64, code string) (bool, error) {
	code = normalizeEmailCode(code)
	if len(code) != 6 {
		return false, nil
	}
	if redis.Client() == nil {
		return false, ErrNoRedis
	}
	n, err := redis.Client().Eval(redis.Ctx, emailCheckScript, []string{emailKey(tenant, userID)}, emailCodeHash(tenant, userID, code), emailMaxTries).Int()
	if err != nil {
		return false, fmt.Errorf("twofa: check email code for user %d: %w", userID, err)
	}
	return n == 1, nil
}
