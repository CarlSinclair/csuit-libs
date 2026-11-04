package redis

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	goredis "github.com/redis/go-redis/v9"
)

const (
	EmailQueueName     = "email_queue"
	EmailQueueDeadName = "email_queue_dead"
	EmailSentLogName   = "email_sent_log"
	EmailErrorLogName  = "email_error_log"
	WebhookQueueName   = "webhook_queue"
	WebhookDeadName    = "webhook_queue_dead"
	TelegramQueueName  = "telegram_queue"
	TelegramDeadName   = "telegram_queue_dead"

	emailLogMaxEntries = 199
)

var enqueueBackoff = []time.Duration{300 * time.Millisecond, 1500 * time.Millisecond}

type EmailAttachment struct {
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type"`
	Data          []byte `json:"data,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

func NewEmailAttachment(filename, contentType string, content []byte) EmailAttachment {
	return EmailAttachment{Filename: filename, ContentType: contentType, Data: content}
}

func (a EmailAttachment) Bytes() ([]byte, error) {
	if len(a.Data) > 0 {
		return a.Data, nil
	}
	if a.ContentBase64 == "" {
		return nil, nil
	}
	out, err := base64.StdEncoding.DecodeString(a.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("decode attachment %q: %w", a.Filename, err)
	}
	return out, nil
}

type EmailTask struct {
	ToEmail      string
	Subject      string
	TemplateName string
	Data         interface{}
	Attachments  []EmailAttachment `json:"attachments,omitempty"`
	OrderID      string            `json:"order_id,omitempty"`
	QueuedAt     string            `json:"queued_at,omitempty"`
	RawJSON      string            `json:"-"`
}

type WebhookJob struct {
	ID         string                 `json:"id"`
	Provider   string                 `json:"provider"`
	EventType  string                 `json:"event_type"`
	Event      map[string]interface{} `json:"event"`
	Payload    string                 `json:"payload"`
	EnqueuedAt time.Time              `json:"enqueued_at"`
	Attempts   int                    `json:"attempts"`
	MaxRetries int                    `json:"max_retries"`
}

type TelegramTask struct {
	ChatID     string    `json:"chat_id"`
	Message    string    `json:"message"`
	ParseMode  string    `json:"parse_mode"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	Attempts   int       `json:"attempts"`
	MaxRetries int       `json:"max_retries"`
}

func requireRedis(operation, queueName string) error {
	if Client() != nil {
		return nil
	}
	if err := Reconnect(errors.New("client not initialized")); err != nil {
		logger.Persistent("redis.require:"+operation, 3, 10*time.Minute, "Redis %s unavailable queue=%s err=%v", operation, queueName, err)
		return fmt.Errorf("redis not initialized: %w", err)
	}
	return nil
}

func stampQueuedAt(task interface{}) interface{} {
	switch t := task.(type) {
	case EmailTask:
		if t.QueuedAt == "" {
			t.QueuedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return t
	case *EmailTask:
		if t != nil && t.QueuedAt == "" {
			t.QueuedAt = time.Now().UTC().Format(time.RFC3339)
		}
		return t
	}
	return task
}

func pushOnce(queueName string, data []byte) error {
	client := Client()
	if client == nil {
		if err := Reconnect(errors.New("client not initialized")); err != nil {
			return fmt.Errorf("redis unavailable: %w", err)
		}
		client = Client()
		if client == nil {
			return errors.New("redis unavailable")
		}
	}
	if err := client.LPush(Ctx, Key(queueName), data).Err(); err != nil {
		if rerr := Reconnect(err); rerr != nil {
			logger.Warn("Redis.Enqueue: reconnect after push failure failed queue=%s err=%v", queueName, rerr)
		}
		return err
	}
	return nil
}

func Enqueue(queueName string, task interface{}) error {
	task = stampQueuedAt(task)
	data, err := json.Marshal(task)
	if err != nil {
		logger.Error("Redis.Enqueue: marshal failed queue=%s type=%T err=%v", queueName, task, err)
		return err
	}
	policy := logger.RetryPolicy{
		Key:       "redis.enqueue:" + queueName,
		Attempts:  3,
		Backoff:   enqueueBackoff,
		Threshold: 3,
		Window:    10 * time.Minute,
	}
	if err := logger.Retry(policy, "Redis.Enqueue queue="+queueName, func() error { return pushOnce(queueName, data) }); err != nil {
		return err
	}
	logger.Debug("Redis.Enqueue: queued queue=%s bytes=%d type=%T", queueName, len(data), task)
	return nil
}

func EnqueueWebhook(provider, eventType string, event map[string]interface{}, payload string) error {
	job := WebhookJob{
		ID:         fmt.Sprintf("%s_%d", provider, time.Now().UnixNano()),
		Provider:   provider,
		EventType:  eventType,
		Event:      event,
		Payload:    payload,
		EnqueuedAt: time.Now(),
		MaxRetries: 5,
	}
	return Enqueue(WebhookQueueName, job)
}

func Dequeue(queueName string, timeout time.Duration) ([]byte, bool, error) {
	if err := requireRedis("Dequeue", queueName); err != nil {
		return nil, false, err
	}
	result, err := Client().BRPop(Ctx, timeout, Key(queueName)).Result()
	if err == goredis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(result) < 2 {
		logger.Warn("Redis.Dequeue: unexpected BRPOP result queue=%s result=%v", queueName, result)
		return nil, false, errors.New("unexpected result format from BRPOP")
	}
	return []byte(result[1]), true, nil
}

func InspectQueue(queueName string) ([]EmailTask, error) {
	if err := requireRedis("InspectQueue", queueName); err != nil {
		return nil, err
	}
	items, err := Client().LRange(Ctx, Key(queueName), 0, -1).Result()
	if err != nil {
		logger.Error("Redis.InspectQueue: LRANGE failed queue=%s err=%v", queueName, err)
		return nil, err
	}
	var tasks []EmailTask
	for _, item := range items {
		var task EmailTask
		if err := json.Unmarshal([]byte(item), &task); err != nil {
			logger.Warn("Redis.InspectQueue: malformed task queue=%s err=%v raw=%s", queueName, err, item)
			tasks = append(tasks, EmailTask{Subject: "Error: Malformed JSON", ToEmail: "N/A", RawJSON: item})
			continue
		}
		task.RawJSON = item
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func ClearQueue(queueName string) (int64, error) {
	if err := requireRedis("ClearQueue", queueName); err != nil {
		return 0, err
	}
	count, err := Client().Del(Ctx, Key(queueName)).Result()
	if err != nil {
		logger.Error("Redis.ClearQueue: DEL failed queue=%s err=%v", queueName, err)
	}
	return count, err
}

func RequeueDeadLetter() (bool, error) {
	if err := requireRedis("RequeueDeadLetter", EmailQueueDeadName); err != nil {
		return false, err
	}
	_, err := Client().RPopLPush(Ctx, Key(EmailQueueDeadName), Key(EmailQueueName)).Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		logger.Error("Redis.RequeueDeadLetter: move failed err=%v", err)
		return false, err
	}
	return true, nil
}

func DeleteDeadLetter(itemToFind string) (int64, error) {
	if err := requireRedis("DeleteDeadLetter", EmailQueueDeadName); err != nil {
		return 0, err
	}
	count, err := Client().LRem(Ctx, Key(EmailQueueDeadName), 1, itemToFind).Result()
	if err != nil {
		logger.Error("Redis.DeleteDeadLetter: LREM failed err=%v", err)
	}
	return count, err
}

func PushDeadLetter(queueName string, payload []byte) error {
	return pushOnce(queueName, payload)
}

func ProcessOneFromQueue(queueName string, senderFunc func(string, string, string, interface{}) error) (bool, error) {
	if err := requireRedis("ProcessOneFromQueue", queueName); err != nil {
		return false, err
	}
	taskData, err := Client().RPop(Ctx, Key(queueName)).Bytes()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		logger.Error("Redis.ProcessOneFromQueue: RPOP failed queue=%s err=%v", queueName, err)
		return false, err
	}
	var task EmailTask
	if err := json.Unmarshal(taskData, &task); err != nil {
		logger.Error("Redis.ProcessOneFromQueue: unmarshal failed queue=%s err=%v raw=%s", queueName, err, string(taskData))
		if pushErr := PushDeadLetter(EmailQueueDeadName, taskData); pushErr != nil {
			logger.Error("Redis.ProcessOneFromQueue: dead-letter push failed queue=%s bytes=%d err=%v", queueName, len(taskData), pushErr)
		}
		return false, err
	}
	logger.Info("Redis.ProcessOneFromQueue: sending to=%q subject=%q", task.ToEmail, task.Subject)
	if err := senderFunc(task.ToEmail, task.Subject, task.TemplateName, task.Data); err != nil {
		logger.Error("Redis.ProcessOneFromQueue: send failed to=%q err=%v, moving to dead-letter queue", task.ToEmail, err)
		if pushErr := PushDeadLetter(EmailQueueDeadName, taskData); pushErr != nil {
			logger.Error("Redis.ProcessOneFromQueue: dead-letter push failed queue=%s bytes=%d err=%v", queueName, len(taskData), pushErr)
		}
		return false, err
	}
	return true, nil
}
