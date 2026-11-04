package logger

import (
	"log"
	"time"
)

func SendTelegramMessage(message string) bool {
	chatID := GetTelegramChatID()
	if GetTelegramBotToken() == "" || chatID == "" {
		return false
	}
	ok, detail := postTelegramRetry(chatID, formatTelegramAlert(message), "MarkdownV2", 10*time.Second)
	if !ok {
		log.Printf("TELEGRAM ERROR: operator message failed: %s", detail)
	}
	return ok
}

func PostTelegram(chatID, message, parseMode string) (ok bool, retryAfter time.Duration, detail string) {
	return postTelegram(chatID, message, parseMode)
}
