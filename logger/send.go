package logger

import (
	"time"
)

func SendTelegramMessage(message string) bool {
	return SendOperatorMessage(message, "MarkdownV2")
}

func PostTelegram(chatID, message, parseMode string) (ok bool, retryAfter time.Duration, detail string) {
	return postTelegram(chatID, message, parseMode)
}
