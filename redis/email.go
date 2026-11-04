package redis

import (
	"encoding/json"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type EmailSentRecord struct {
	Timestamp    string `json:"ts"`
	ToEmail      string `json:"to_email"`
	Subject      string `json:"subject"`
	TemplateName string `json:"template_name"`
	Attempts     int    `json:"attempts"`
}

type EmailErrorRecord struct {
	Timestamp    string `json:"ts"`
	ToEmail      string `json:"to_email"`
	Subject      string `json:"subject"`
	TemplateName string `json:"template_name"`
	Attempt      int    `json:"attempt"`
	Error        string `json:"error"`
	RawTask      string `json:"raw_task,omitempty"`
}

type EmailDeliveryState struct {
	Status string
	Detail string
}

func appendLog(what, name string, payload []byte) {
	client := Client()
	if client == nil {
		return
	}
	key := Key(name)
	if err := client.LPush(Ctx, key, payload).Err(); err != nil {
		logger.Persistent("redis.emaillog:"+name, 3, 10*time.Minute, "%s: LPUSH failed key=%s err=%v", what, key, err)
		return
	}
	logger.ResetPersistent("redis.emaillog:" + name)
	if err := client.LTrim(Ctx, key, 0, emailLogMaxEntries).Err(); err != nil {
		logger.Warn("%s: LTRIM failed key=%s err=%v", what, key, err)
	}
}

func RecordEmailSent(task EmailTask, attempts int) {
	rec := EmailSentRecord{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		ToEmail:      task.ToEmail,
		Subject:      task.Subject,
		TemplateName: task.TemplateName,
		Attempts:     attempts,
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		logger.Error("RecordEmailSent: marshal failed to=%q err=%v", task.ToEmail, err)
		return
	}
	appendLog("RecordEmailSent", EmailSentLogName, payload)
}

func RecordEmailError(task EmailTask, attempt int, sendErr error) {
	if sendErr == nil {
		return
	}
	rec := EmailErrorRecord{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		ToEmail:      task.ToEmail,
		Subject:      task.Subject,
		TemplateName: task.TemplateName,
		Attempt:      attempt,
		Error:        sendErr.Error(),
	}
	if taskJSON, err := json.Marshal(task); err != nil {
		logger.Error("RecordEmailError: task marshal failed to=%q err=%v", task.ToEmail, err)
	} else {
		rec.RawTask = string(taskJSON)
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		logger.Error("RecordEmailError: marshal failed to=%q err=%v", task.ToEmail, err)
		return
	}
	appendLog("RecordEmailError", EmailErrorLogName, payload)
}

func EmailDeliveryStatus(toEmail, templateName string, since time.Time) EmailDeliveryState {
	client := Client()
	if client == nil {
		return EmailDeliveryState{Status: "unknown", Detail: "redis is not available"}
	}
	since = since.UTC().Truncate(time.Second)

	sentKey := Key(EmailSentLogName)
	sent, err := client.LRange(Ctx, sentKey, 0, 99).Result()
	if err != nil {
		logger.Error("EmailDeliveryStatus: reading %s failed to=%q: %v", sentKey, toEmail, err)
		return EmailDeliveryState{Status: "unknown", Detail: "reading the sent log failed: " + err.Error()}
	}
	for _, raw := range sent {
		var rec EmailSentRecord
		if json.Unmarshal([]byte(raw), &rec) != nil || rec.ToEmail != toEmail || rec.TemplateName != templateName {
			continue
		}
		if ts, perr := time.Parse(time.RFC3339, rec.Timestamp); perr == nil && !ts.Before(since) {
			return EmailDeliveryState{Status: "sent"}
		}
	}

	deadKey := Key(EmailQueueDeadName)
	dead, err := client.LRange(Ctx, deadKey, 0, -1).Result()
	if err != nil {
		logger.Error("EmailDeliveryStatus: reading %s failed to=%q: %v", deadKey, toEmail, err)
	}
	lastErr := ""
	errKey := Key(EmailErrorLogName)
	errs, err := client.LRange(Ctx, errKey, 0, 99).Result()
	if err != nil {
		logger.Error("EmailDeliveryStatus: reading %s failed to=%q: %v", errKey, toEmail, err)
	}
	for _, raw := range errs {
		var rec EmailErrorRecord
		if json.Unmarshal([]byte(raw), &rec) != nil || rec.ToEmail != toEmail || rec.TemplateName != templateName {
			continue
		}
		if ts, perr := time.Parse(time.RFC3339, rec.Timestamp); perr == nil && !ts.Before(since) {
			lastErr = rec.Error
			break
		}
	}
	for _, raw := range dead {
		var t EmailTask
		if json.Unmarshal([]byte(raw), &t) != nil || t.ToEmail != toEmail || t.TemplateName != templateName {
			continue
		}
		if qa, perr := time.Parse(time.RFC3339, t.QueuedAt); perr == nil && !qa.Before(since) {
			if lastErr == "" {
				lastErr = "dead-lettered with no error recorded"
			}
			return EmailDeliveryState{Status: "failed", Detail: lastErr}
		}
	}
	return EmailDeliveryState{Status: "queued", Detail: lastErr}
}
