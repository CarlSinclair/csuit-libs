package logger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

var TelegramBaseURL = ""

var TelegramBaseOverrideFn func() string

func TelegramBase() string {
	if fn := TelegramBaseOverrideFn; fn != nil {
		if v := strings.TrimRight(strings.TrimSpace(fn()), "/"); v != "" {
			noteTelegramBase(v)
			return v
		}
	}
	return strings.TrimRight(strings.TrimSpace(TelegramBaseURL), "/")
}

var (
	TelegramBotTokenFn func() string
	TelegramChatIDFn   func() string
)

func GetTelegramBotToken() string {
	if TelegramBotTokenFn != nil {
		return TelegramBotTokenFn()
	}
	return ""
}

func GetTelegramChatID() string {
	if TelegramChatIDFn != nil {
		return TelegramChatIDFn()
	}
	return ""
}

func EscapeMarkdownV2(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]", "(", "\\(",
		")", "\\)", "~", "\\~", "`", "\\`", ">", "\\>", "#", "\\#",
		"+", "\\+", "-", "\\-", "=", "\\=", "|", "\\|", "{", "\\{",
		"}", "\\}", ".", "\\.", "!", "\\!",
	)
	return replacer.Replace(text)
}

func SendTelegramMessageSync(message string) {
	chatID := GetTelegramChatID()
	if GetTelegramBotToken() == "" || chatID == "" {
		return
	}

	sendTelegramSync(chatID, formatTelegramAlert(message), "MarkdownV2")
}

func SendToUserSync(chatID string, message string) {
	if GetTelegramBotToken() == "" || chatID == "" {
		return
	}
	sendTelegramSync(chatID, message, "HTML")
}

func formatTelegramAlert(message string) string {
	return fmt.Sprintf("*%s ALERT \\[%s\\]*\n```\n%s\n```", strings.ToUpper(cfg.AppName), EscapeMarkdownV2(serviceName), EscapeMarkdownV2(message))
}

func postTelegram(chatID, message, parseMode string) (ok bool, retryAfter time.Duration, detail string) {
	chatID = resolveMigratedChat(chatID)
	botToken := GetTelegramBotToken()
	if botToken == "" || chatID == "" {
		return false, 0, "not configured"
	}

	base := TelegramBase()
	if base == "" {
		return false, 0, "telegram proxy base not configured"
	}
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", base, botToken)

	payload, err := json.Marshal(map[string]string{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": parseMode,
	})
	if err != nil {
		return false, 0, fmt.Sprintf("marshal failed: %v", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(payload))
	if err != nil {
		return false, 0, fmt.Sprintf("request creation failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, 0, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusOK {
		return true, 0, ""
	}
	detail = fmt.Sprintf("API status %d chat_id=%s body=%s", resp.StatusCode, chatID, strings.TrimSpace(string(body)))
	if rerr != nil {
		detail += fmt.Sprintf(" body_read_err=%v", rerr)
	}

	if newID := MigratedChatID(body); newID != "" && newID != chatID {
		ApplyChatMigration(chatID, newID)
		return postTelegram(newID, message, parseMode)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		var parsed struct {
			RetryAfter int `json:"retry_after"`
			Parameters struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		if jerr := json.Unmarshal(body, &parsed); jerr != nil {
			detail += fmt.Sprintf(" retry_after_unreadable=%v", jerr)
		}
		secs := parsed.Parameters.RetryAfter
		if secs == 0 {
			secs = parsed.RetryAfter
		}
		if secs <= 0 {
			secs = 5
		}
		return false, time.Duration(secs) * time.Second, detail
	}
	return false, 0, detail
}

func sendTelegramSync(chatID, message, parseMode string) {
	if GetTelegramBotToken() == "" || chatID == "" {
		return
	}
	if ok, _, detail := postTelegram(chatID, message, parseMode); !ok {
		Error("sendTelegramSync: %s", detail)
	}
}

const (
	telegramDedupWindow  = 45 * time.Second
	telegramMinInterval  = 2 * time.Second
	telegramRetryCap     = 60 * time.Second
	telegramQueueDepth   = 128
	telegramMaxDedupKeys = 1000
)

type telegramDedupEntry struct {
	first  time.Time
	count  int
	sample string
}

var (
	telegramQueue     chan string
	telegramQueueOnce sync.Once

	telegramSeenMu sync.Mutex
	telegramSeen   = map[[32]byte]*telegramDedupEntry{}

	telegramDropped int64
)

func postTelegramRetry(chatID, message, parseMode string, maxWait time.Duration) (bool, string) {
	ok, retryAfter, detail := postTelegram(chatID, message, parseMode)
	if !ok && retryAfter > 0 {
		if retryAfter > maxWait {
			retryAfter = maxWait
		}
		time.Sleep(retryAfter)
		ok, _, detail = postTelegram(chatID, message, parseMode)
	}
	return ok, Redact(detail)
}

func EnqueueTelegramAlert(message string) {
	enqueueTelegramAlert(message)
}

func enqueueTelegramAlert(message string) {
	if GetTelegramBotToken() == "" || GetTelegramChatID() == "" {
		return
	}
	telegramQueueOnce.Do(func() {
		telegramQueue = make(chan string, telegramQueueDepth)
		go telegramSender()
	})

	key := telegramDedupKey(message)
	now := time.Now()

	telegramSeenMu.Lock()
	if e := telegramSeen[key]; e != nil && now.Sub(e.first) < telegramDedupWindow {
		e.count++
		telegramSeenMu.Unlock()
		return
	}
	if len(telegramSeen) < telegramMaxDedupKeys {
		telegramSeen[key] = &telegramDedupEntry{first: now, count: 1, sample: message}
	}
	telegramSeenMu.Unlock()

	select {
	case telegramQueue <- message:
	default:
		atomic.AddInt64(&telegramDropped, 1)
	}
}

func telegramSender() {
	lastSent := time.Now().Add(-telegramMinInterval)
	send := func(msg string) {
		if wait := telegramMinInterval - time.Since(lastSent); wait > 0 {
			time.Sleep(wait)
		}
		chatID := GetTelegramChatID()
		formatted := formatTelegramAlert(msg)
		ok, retryAfter, detail := postTelegram(chatID, formatted, "MarkdownV2")
		if !ok && retryAfter > 0 {
			if retryAfter > telegramRetryCap {
				retryAfter = telegramRetryCap
			}
			time.Sleep(retryAfter)
			ok, _, detail = postTelegram(chatID, formatted, "MarkdownV2")
		}
		if !ok {
			log.Printf("TELEGRAM ERROR: alert send failed: %s", Redact(detail))
		}
		lastSent = time.Now()
	}

	sweep := time.NewTicker(10 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case msg := <-telegramQueue:
			if dropped := atomic.SwapInt64(&telegramDropped, 0); dropped > 0 {
				send(fmt.Sprintf("[logger] %d further alert(s) dropped - Telegram send backlog", dropped))
			}
			send(msg)
		case <-sweep.C:
			for _, summary := range telegramFlushExpiredDupes() {
				send(summary)
			}
		}
	}
}

func telegramFlushExpiredDupes() []string {
	now := time.Now()
	var out []string
	telegramSeenMu.Lock()
	for k, e := range telegramSeen {
		if now.Sub(e.first) < telegramDedupWindow {
			continue
		}
		if e.count > 1 {
			out = append(out, fmt.Sprintf("%s\n\n(repeated %d times in the last %s)", e.sample, e.count-1, telegramDedupWindow))
		}
		delete(telegramSeen, k)
	}
	telegramSeenMu.Unlock()
	return out
}

var telegramCallSiteRe = regexp.MustCompile(`^\S+ \S+ (\[[^\]]*\] \S+:[0-9]+ [A-Z]+):( PERSISTENT \S+)?`)

func telegramDedupKey(message string) [32]byte {
	if m := telegramCallSiteRe.FindStringSubmatch(message); m != nil {
		return sha256.Sum256([]byte("site:" + m[1] + m[2]))
	}
	var b strings.Builder
	b.Grow(len(message))
	prevDigit := false
	for _, r := range message {
		if r >= '0' && r <= '9' {
			if !prevDigit {
				b.WriteByte('#')
			}
			prevDigit = true
			continue
		}
		prevDigit = false
		b.WriteRune(r)
	}
	return sha256.Sum256([]byte(b.String()))
}
