package redis

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/mailer"
	goredis "github.com/redis/go-redis/v9"
)

var emailRetryDelays = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

type EmailWorkerConfig struct {
	Workers   int
	PerSecond float64
}

var EmailWorkerConfigFn func() EmailWorkerConfig

func emailWorkerConfig() EmailWorkerConfig {
	cfg := EmailWorkerConfig{Workers: 4, PerSecond: 5}
	if EmailWorkerConfigFn != nil {
		c := EmailWorkerConfigFn()
		if c.Workers > 0 {
			cfg.Workers = c.Workers
		}
		if c.PerSecond > 0 {
			cfg.PerSecond = c.PerSecond
		}
	}
	return cfg
}

func (t EmailTask) MailerAttachments() ([]mailer.Attachment, error) {
	out := make([]mailer.Attachment, 0, len(t.Attachments))
	for i, a := range t.Attachments {
		content, err := a.Bytes()
		if err != nil {
			return nil, fmt.Errorf("attachment %d (%q) decode failed: %w", i, a.Filename, err)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("attachment %d (%q) is empty", i, a.Filename)
		}
		out = append(out, mailer.Attachment{Filename: a.Filename, ContentType: a.ContentType, Content: content})
	}
	return out, nil
}

func StartEmailWorker(send func(EmailTask) error, permanent func(error) bool) {
	cfg := emailWorkerConfig()
	interval := time.Duration(float64(time.Second) / cfg.PerSecond)
	tokens := make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			select {
			case tokens <- struct{}{}:
			default:
			}
		}
	}()
	logger.Info("Email Worker started workers=%d per_second=%.2f", cfg.Workers, cfg.PerSecond)
	for i := 0; i < cfg.Workers; i++ {
		go emailWorkerLoop(i, send, permanent, tokens)
	}
	go emailRetryMover()
}

func emailWorkerLoop(id int, send func(EmailTask) error, permanent func(error) bool, tokens <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Email Worker %d crashed: %v. Restarting...", id, r)
			logger.SentryRecover(r)
			time.Sleep(5 * time.Second)
			go emailWorkerLoop(id, send, permanent, tokens)
		}
	}()

	for {
		if !IsReady() {
			logger.Persistent("email.worker.redis", 5, 10*time.Minute, "Email Worker: Redis unavailable; waiting before dequeue diag=%s", Diagnostics())
			if rerr := Reconnect(fmt.Errorf("redis unavailable before email dequeue")); rerr != nil {
				logger.Persistent("email.worker.reconnect", 5, 10*time.Minute, "Email Worker: redis reconnect failed: %v", rerr)
			}
			time.Sleep(5 * time.Second)
			continue
		}

		payload, found, err := Dequeue(EmailQueueName, 10*time.Second)
		if err != nil {
			logger.Persistent("email.worker.dequeue", 5, 10*time.Minute, "Email Worker: dequeue failed queue=%s err=%v diag=%s", EmailQueueName, err, Diagnostics())
			Backoff(2 * time.Second)
			continue
		}
		logger.ResetPersistent("email.worker.dequeue")
		if !found {
			continue
		}

		var task EmailTask
		if err := json.Unmarshal(payload, &task); err != nil {
			logger.Error("Email Worker: invalid JSON task queue=%s err=%v raw=%s", EmailQueueName, err, string(payload))
			deadLetterEmail(payload, "invalid JSON", "")
			continue
		}

		<-tokens
		processEmailTask(task, payload, send, permanent)
	}
}

func processEmailTask(task EmailTask, payload []byte, send func(EmailTask) error, permanent func(error) bool) {
	attempt := task.Attempts + 1
	err := send(task)
	if err == nil {
		RecordEmailSent(task, attempt)
		logger.ResetPersistent("email.worker.send")
		logger.Info("Email Worker: sent to=%q template=%q attempts=%d", task.ToEmail, task.TemplateName, attempt)
		return
	}
	RecordEmailError(task, attempt, err)
	logger.Persistent("email.worker.send", 3, time.Hour, "Email Worker: send failing to=%q template=%q attempt=%d err=%v", task.ToEmail, task.TemplateName, attempt, err)

	isPermanent := permanent != nil && permanent(err)
	if isPermanent || task.Attempts >= len(emailRetryDelays) {
		logger.Error("Email Worker: giving up to=%q template=%q attempts=%d permanent=%t last_err=%v. Moving to DLQ.", task.ToEmail, task.TemplateName, attempt, isPermanent, err)
		deadLetterEmail(payload, task.TemplateName, task.ToEmail)
		return
	}

	delay := emailRetryDelays[task.Attempts]
	task.Attempts++
	scheduleEmailRetry(task, delay, payload)
}

func scheduleEmailRetry(task EmailTask, delay time.Duration, original []byte) {
	data, err := json.Marshal(task)
	if err != nil {
		logger.Error("Email Worker: retry marshal failed to=%q template=%q err=%v", task.ToEmail, task.TemplateName, err)
		deadLetterEmail(original, task.TemplateName, task.ToEmail)
		return
	}
	due := float64(time.Now().Add(delay).Unix())
	if err := requireRedis("ScheduleEmailRetry", EmailQueueRetryName); err == nil {
		if zerr := Client().ZAdd(Ctx, Key(EmailQueueRetryName), goredis.Z{Score: due, Member: string(data)}).Err(); zerr == nil {
			logger.Warn("Email Worker: retry scheduled to=%q template=%q attempt=%d in=%s", task.ToEmail, task.TemplateName, task.Attempts, delay)
			return
		} else {
			logger.Error("Email Worker: scheduling retry failed to=%q template=%q err=%v, requeueing immediately", task.ToEmail, task.TemplateName, zerr)
		}
	}
	if eerr := Enqueue(EmailQueueName, task); eerr != nil {
		logger.Error("Email Worker: could not requeue to=%q template=%q err=%v. Moving to DLQ.", task.ToEmail, task.TemplateName, eerr)
		deadLetterEmail(original, task.TemplateName, task.ToEmail)
	}
}

func emailRetryMover() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Email retry mover crashed: %v. Restarting...", r)
			logger.SentryRecover(r)
			time.Sleep(5 * time.Second)
			go emailRetryMover()
		}
	}()
	for {
		time.Sleep(5 * time.Second)
		if !IsReady() {
			continue
		}
		now := fmt.Sprintf("%d", time.Now().Unix())
		items, err := Client().ZRangeByScore(Ctx, Key(EmailQueueRetryName), &goredis.ZRangeBy{Min: "-inf", Max: now, Count: 200}).Result()
		if err != nil {
			logger.Persistent("email.retry.mover", 5, 10*time.Minute, "Email retry mover: read failed err=%v", err)
			continue
		}
		logger.ResetPersistent("email.retry.mover")
		for _, item := range items {
			removed, rerr := Client().ZRem(Ctx, Key(EmailQueueRetryName), item).Result()
			if rerr != nil {
				logger.Error("Email retry mover: remove failed err=%v", rerr)
				continue
			}
			if removed == 0 {
				continue
			}
			if perr := pushOnce(EmailQueueName, []byte(item)); perr != nil {
				if serr := spoolAppend(EmailQueueName, []byte(item)); serr != nil {
					logger.Error("Email retry mover: requeue and spool failed err=%v spool_err=%v item=%s", perr, serr, item)
				}
			}
		}
	}
}

func deadLetterEmail(payload []byte, template, to string) {
	if err := PushDeadLetter(EmailQueueDeadName, payload); err != nil {
		logger.Error("Email Worker: failed moving task to DLQ queue=%s to=%q template=%q err=%v", EmailQueueDeadName, to, template, err)
		return
	}
	logger.Error("Email Worker: task moved to DLQ queue=%s to=%q template=%q", EmailQueueDeadName, to, template)
}
