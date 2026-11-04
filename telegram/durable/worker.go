package durable

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/CarlSinclair/csuit-libs/telegram"
)

const maxAttempts = 4

type WorkerConfig struct {
	TokenFor func(botRef string) string
	Enabled  func() bool

	RequireDefaultToken bool
}

func Enqueue(botRef, chatID, message, parseMode string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return errors.New("telegram enqueue: empty chat id")
	}
	return redis.Enqueue(redis.TelegramQueueName, redis.TelegramTask{
		ChatID:     chatID,
		Message:    message,
		ParseMode:  parseMode,
		EnqueuedAt: time.Now(),
		MaxRetries: maxAttempts,
		BotRef:     botRef,
	})
}

func StartWorker(cfg WorkerConfig) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Telegram worker crashed: %v. Restarting...", r)
				logger.SentryRecover(r)
				time.Sleep(5 * time.Second)
				StartWorker(cfg)
			}
		}()
		runWorker(cfg)
	}()
}

func deadLetter(payload []byte, why string) {
	if err := redis.PushDeadLetter(redis.TelegramDeadName, payload); err != nil {
		logger.Error("Telegram worker: dead-letter push failed (%s), item stays in processing list err=%v", why, err)
		return
	}
	if err := redis.Ack(redis.TelegramQueueName, payload); err != nil {
		logger.Warn("Telegram worker: ack after dead-letter failed (%s) err=%v", why, err)
	}
}

func runWorker(cfg WorkerConfig) {
	if cfg.TokenFor == nil {
		logger.Error("Telegram worker: no TokenFor configured; worker not started")
		return
	}
	for redis.Client() == nil {
		time.Sleep(2 * time.Second)
	}
	if n, err := redis.RecoverProcessing(redis.TelegramQueueName); err != nil {
		logger.Error("Telegram worker: recovering in-flight messages failed recovered=%d err=%v", n, err)
	} else if n > 0 {
		logger.Warn("Telegram worker: requeued %d in-flight message(s) left by a previous run", n)
	}
	logger.Info("Telegram worker started")
	for {
		if cfg.Enabled != nil && !cfg.Enabled() {
			time.Sleep(10 * time.Second)
			continue
		}
		if cfg.RequireDefaultToken && strings.TrimSpace(cfg.TokenFor("")) == "" {
			time.Sleep(10 * time.Second)
			continue
		}
		payload, found, err := redis.DequeueReliable(redis.TelegramQueueName, 10*time.Second)
		if err != nil {
			logger.Persistent("telegram.worker.dequeue", 5, 10*time.Minute, "Telegram worker: dequeue failed queue=%s err=%v", redis.TelegramQueueName, err)
			time.Sleep(2 * time.Second)
			continue
		}
		logger.ResetPersistent("telegram.worker.dequeue")
		if !found {
			continue
		}
		var task redis.TelegramTask
		if err := json.Unmarshal(payload, &task); err != nil {
			logger.Error("Telegram worker: invalid JSON task, moving to DLQ: %v", err)
			deadLetter(payload, "invalid json")
			continue
		}
		task.ChatID = strings.TrimSpace(task.ChatID)
		if task.ChatID == "" {
			logger.Error("Telegram worker: empty chat_id in task, moving to DLQ")
			deadLetter(payload, "empty chat id")
			continue
		}
		token := strings.TrimSpace(cfg.TokenFor(task.BotRef))
		if token == "" {
			logger.Error("Telegram worker: no bot token for bot_ref=%q chat=%s, moving to DLQ", task.BotRef, task.ChatID)
			deadLetter(payload, "no token")
			continue
		}
		submit(token, task, payload)
	}
}

func submit(token string, task redis.TelegramTask, payload []byte) {
	job := telegram.Job{
		Op: "worker send chat=" + task.ChatID,
		Run: func(ctx context.Context, b *telegram.Bot) error {
			return b.SendWithMode(ctx, task.ChatID, task.Message, task.ParseMode)
		},
		Done: func(sendErr error) {
			if sendErr != nil {
				logger.Error("Telegram worker: giving up sending to %s, moving to DLQ: %v", task.ChatID, sendErr)
				deadLetter(payload, "send failed")
				return
			}
			if err := redis.Ack(redis.TelegramQueueName, payload); err != nil {
				logger.Warn("Telegram worker: ack after send failed chat=%s err=%v", task.ChatID, err)
			}
		},
	}
	for {
		err := telegram.Submit(token, job)
		if errors.Is(err, telegram.ErrQueueFull) {
			time.Sleep(time.Second)
			continue
		}
		if err != nil {
			logger.Error("Telegram worker: could not queue send to %s, moving to DLQ: %v", task.ChatID, err)
			deadLetter(payload, "submit failed")
		}
		return
	}
}
