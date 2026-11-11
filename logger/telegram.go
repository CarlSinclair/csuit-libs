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

var TelegramDurableFn func(chatID, text, parseMode string) error

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

	gated, ok := alertGate(message, alertExactKey(message), true)
	if !ok {
		return
	}
	sendTelegramSync(chatID, formatTelegramAlert(gated), "MarkdownV2")
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
	telegramMinInterval = 2 * time.Second
	telegramRetryCap    = 60 * time.Second
	telegramQueueDepth  = 128
)

var (
	telegramQueue     chan string
	telegramQueueOnce sync.Once
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
	enqueueTelegramAlert(message, "")
}

var (
	alertFilterMu sync.RWMutex
	alertFilter   func(message string) bool
)

var outageDropped int64

func TakeSuppressedCount() int64 {
	return atomic.SwapInt64(&outageDropped, 0)
}

func SetAlertSuppressor(f func(message string) bool) {
	alertFilterMu.Lock()
	alertFilter = f
	alertFilterMu.Unlock()
}

func alertSuppressed(message string) bool {
	alertFilterMu.RLock()
	f := alertFilter
	alertFilterMu.RUnlock()
	return f != nil && f(message)
}

func SendStateAlert(stateKey, message string) {
	enqueueAlertUnfiltered(message, "state:"+stateKey)
}

func enqueueTelegramAlert(message, alertKey string) {
	if alertSuppressed(message) {
		return
	}
	enqueueAlertUnfiltered(message, alertKey)
}

func enqueueAlertUnfiltered(message, alertKey string) {
	if GetTelegramBotToken() == "" || GetTelegramChatID() == "" {
		return
	}
	telegramQueueOnce.Do(func() {
		telegramQueue = make(chan string, telegramQueueDepth)
		go telegramSender()
	})

	gated, ok := alertGate(message, alertCollapsedKey(message, alertKey), false)
	if !ok {
		return
	}
	message = gated

	select {
	case telegramQueue <- message:
	default:
		go func() { telegramQueue <- message }()
	}
}

func telegramSender() {
	lastSent := time.Now().Add(-telegramMinInterval)
	send := func(msg string) {
		if fn := TelegramDurableFn; fn != nil {
			if err := fn(GetTelegramChatID(), formatTelegramAlert(msg), "MarkdownV2"); err == nil {
				return
			} else {
				log.Printf("TELEGRAM ERROR: durable enqueue failed, sending directly: %s", Redact(err.Error()))
			}
		}
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
			send(msg)
		case <-sweep.C:
			for _, summary := range alertSweep() {
				send(summary)
			}
		}
	}
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
