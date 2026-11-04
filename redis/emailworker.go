package redis

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/mailer"
)

var emailRetryDelays = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

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
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Email Worker crashed: %v. Restarting...", r)
				logger.SentryRecover(r)
				time.Sleep(5 * time.Second)
				StartEmailWorker(send, permanent)
			}
		}()

		logger.Info("Email Worker started")

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
				time.Sleep(2 * time.Second)
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

			attempt := 0
			lastErr := logger.Retry(logger.RetryPolicy{
				Key:       "email.worker.send",
				Attempts:  len(emailRetryDelays) + 1,
				Backoff:   emailRetryDelays,
				Threshold: 3,
				Window:    time.Hour,
				Permanent: permanent,
			}, fmt.Sprintf("Email Worker: send to=%q template=%q attachments=%d", task.ToEmail, task.TemplateName, len(task.Attachments)), func() error {
				attempt++
				serr := send(task)
				if serr != nil {
					RecordEmailError(task, attempt, serr)
					return serr
				}
				RecordEmailSent(task, attempt)
				return nil
			})

			if lastErr == nil {
				logger.Info("Email Worker: sent to=%q template=%q attempts=%d", task.ToEmail, task.TemplateName, attempt)
				continue
			}
			logger.Error("Email Worker: permanent failure to=%q template=%q last_err=%v. Moving to DLQ.", task.ToEmail, task.TemplateName, lastErr)
			deadLetterEmail(payload, task.TemplateName, task.ToEmail)
		}
	}()
}

func deadLetterEmail(payload []byte, template, to string) {
	if err := PushDeadLetter(EmailQueueDeadName, payload); err != nil {
		logger.Error("Email Worker: failed moving task to DLQ queue=%s to=%q template=%q err=%v", EmailQueueDeadName, to, template, err)
		return
	}
	logger.Error("Email Worker: task moved to DLQ queue=%s to=%q template=%q", EmailQueueDeadName, to, template)
}
