package logger

import (
	"regexp"
	"strings"
	"sync/atomic"
)

var (
	telegramProxyURLRe = regexp.MustCompile(`https?://[^\s"'/]+/bot[0-9]{6,}:[A-Za-z0-9_-]{20,}`)
	telegramBotTokenRe = regexp.MustCompile(`[0-9]{6,}:[A-Za-z0-9_-]{30,}`)

	lastTelegramBase atomic.Value
)

func noteTelegramBase(base string) {
	if base != "" {
		lastTelegramBase.Store(base)
	}
}

func Redact(s string) string {
	if s == "" {
		return s
	}
	s = telegramProxyURLRe.ReplaceAllString(s, "[TELEGRAM_PROXY]/bot[REDACTED_BOT_TOKEN]")
	s = telegramBotTokenRe.ReplaceAllString(s, "[REDACTED_BOT_TOKEN]")
	if base := strings.TrimSpace(TelegramBaseURL); base != "" {
		s = strings.ReplaceAll(s, base, "[TELEGRAM_PROXY]")
	}
	if v, ok := lastTelegramBase.Load().(string); ok && v != "" {
		s = strings.ReplaceAll(s, v, "[TELEGRAM_PROXY]")
	}
	return redactIPs(s)
}
