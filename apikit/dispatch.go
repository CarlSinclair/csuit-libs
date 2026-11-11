package apikit

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

const (
	defaultQueue        = "api_webhook_deliveries"
	defaultDisableAfter = 15
	retryTick           = 10 * time.Second
	pruneEvery          = time.Hour
	deliveryRetention   = 14 * 24 * time.Hour
)

var defaultRetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}

type Emitter struct {
	Hooks        WebhookStore
	Open         func(sealed string) (string, error)
	Client       *http.Client
	Queue        string
	DisableAfter int
	RetryDelays  []time.Duration
}

type job struct {
	WebhookID int64  `json:"webhook_id"`
	EventID   string `json:"event_id"`
	Type      string `json:"type"`
	Body      string `json:"body"`
	Attempt   int    `json:"attempt"`
}

func (e *Emitter) queue() string {
	if e.Queue != "" {
		return e.Queue
	}
	return defaultQueue
}

func (e *Emitter) retryKey() string { return e.queue() + ":retry" }

func (e *Emitter) delays() []time.Duration {
	if len(e.RetryDelays) > 0 {
		return e.RetryDelays
	}
	return defaultRetryDelays
}

func (e *Emitter) disableAfter() int {
	if e.DisableAfter > 0 {
		return e.DisableAfter
	}
	return defaultDisableAfter
}

func (e *Emitter) Emit(owner int64, eventType string, data any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hooks, err := e.Hooks.ListFor(ctx, owner, eventType)
	if err != nil {
		logger.Error("apikit: webhook emit lookup failed owner=%d event=%s err=%v", owner, eventType, err)
		return
	}
	if len(hooks) == 0 {
		return
	}
	id, err := NewEventID()
	if err != nil {
		logger.Error("apikit: webhook emit event id failed owner=%d event=%s err=%v", owner, eventType, err)
		return
	}
	body, err := json.Marshal(Event{ID: id, Type: eventType, CreatedAt: time.Now().UTC(), Data: data})
	if err != nil {
		logger.Error("apikit: webhook emit encode failed owner=%d event=%s err=%v", owner, eventType, err)
		return
	}
	for _, h := range hooks {
		j := job{WebhookID: h.ID, EventID: id, Type: eventType, Body: string(body), Attempt: 1}
		if err := redis.Enqueue(e.queue(), j); err != nil {
			logger.Error("apikit: webhook enqueue failed webhook_id=%d event_id=%s event=%s err=%v", h.ID, id, eventType, err)
		}
	}
}

func (e *Emitter) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return NewSafeClient(nil)
}

func (e *Emitter) Run(ctx context.Context) {
	if n, err := redis.RecoverProcessing(e.queue()); err != nil {
		logger.Error("apikit: webhook recover processing failed err=%v", err)
	} else if n > 0 {
		logger.Info("apikit: webhook deliveries recovered from processing list count=%d", n)
	}
	go e.retryLoop(ctx)
	for ctx.Err() == nil {
		payload, ok, err := redis.DequeueReliable(e.queue(), 5*time.Second)
		if err != nil {
			logger.Persistent("apikit.webhook.dequeue", 3, 10*time.Minute, "apikit: webhook dequeue failed err=%v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		logger.ResetPersistent("apikit.webhook.dequeue")
		if !ok {
			continue
		}
		e.handle(ctx, payload)
		if err := redis.Ack(e.queue(), payload); err != nil {
			logger.Warn("apikit: webhook ack failed err=%v", err)
		}
	}
}

func (e *Emitter) handle(ctx context.Context, payload []byte) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("apikit: webhook delivery panic: %v", r)
		}
	}()
	var j job
	if err := json.Unmarshal(payload, &j); err != nil {
		logger.Error("apikit: webhook job undecodable, dropped err=%v", err)
		return
	}
	hook, err := e.Hooks.Get(ctx, j.WebhookID)
	if err != nil {
		logger.Error("apikit: webhook load failed webhook_id=%d event_id=%s err=%v", j.WebhookID, j.EventID, err)
		e.scheduleRetry(j, "load webhook")
		return
	}
	if hook == nil || !hook.Enabled {
		logger.Info("apikit: webhook delivery skipped, subscription gone or disabled webhook_id=%d event_id=%s", j.WebhookID, j.EventID)
		return
	}
	secret, err := e.Open(hook.SecretEnc)
	if err != nil {
		logger.Error("apikit: webhook secret unreadable webhook_id=%d event_id=%s err=%v", hook.ID, j.EventID, err)
		e.record(ctx, Delivery{WebhookID: hook.ID, EventID: j.EventID, EventType: j.Type, Attempt: j.Attempt, Error: "secret unreadable"})
		return
	}
	res := Send(ctx, e.client(), hook.URL, secret, j.EventID, j.Type, []byte(j.Body))
	e.record(ctx, Delivery{
		WebhookID: hook.ID, EventID: j.EventID, EventType: j.Type, Attempt: j.Attempt,
		StatusCode: res.StatusCode, OK: res.OK, Error: res.Error, DurationMS: int(res.Duration / time.Millisecond),
	})
	if res.OK {
		e.result(ctx, hook.ID, true)
		return
	}
	logger.Warn("apikit: webhook delivery failed webhook_id=%d event_id=%s attempt=%d status=%d err=%s", hook.ID, j.EventID, j.Attempt, res.StatusCode, res.Error)
	if !e.scheduleRetry(j, res.Error) {
		e.result(ctx, hook.ID, false)
	}
}

func (e *Emitter) record(ctx context.Context, d Delivery) {
	if err := e.Hooks.AddDelivery(ctx, d); err != nil {
		logger.Error("apikit: webhook delivery log failed webhook_id=%d event_id=%s err=%v", d.WebhookID, d.EventID, err)
	}
}

func (e *Emitter) result(ctx context.Context, id int64, ok bool) {
	disabled, err := e.Hooks.RecordResult(ctx, id, ok, e.disableAfter())
	if err != nil {
		logger.Error("apikit: webhook result record failed webhook_id=%d err=%v", id, err)
		return
	}
	if disabled {
		logger.Warn("apikit: webhook disabled after repeated failures webhook_id=%d", id)
	}
}

func (e *Emitter) scheduleRetry(j job, reason string) bool {
	delays := e.delays()
	if j.Attempt > len(delays) {
		logger.Warn("apikit: webhook delivery given up webhook_id=%d event_id=%s attempts=%d reason=%s", j.WebhookID, j.EventID, j.Attempt, reason)
		return false
	}
	delay := delays[j.Attempt-1]
	j.Attempt++
	buf, err := json.Marshal(j)
	if err != nil {
		logger.Error("apikit: webhook retry encode failed webhook_id=%d event_id=%s err=%v", j.WebhookID, j.EventID, err)
		return false
	}
	client := redis.Client()
	if client == nil {
		logger.Error("apikit: webhook retry not scheduled, redis unavailable webhook_id=%d event_id=%s", j.WebhookID, j.EventID)
		return false
	}
	score := float64(time.Now().Add(delay).Unix())
	if err := client.ZAdd(redis.Ctx, redis.Key(e.retryKey()), goredis.Z{Score: score, Member: string(buf)}).Err(); err != nil {
		logger.Error("apikit: webhook retry schedule failed webhook_id=%d event_id=%s err=%v", j.WebhookID, j.EventID, err)
		return false
	}
	return true
}

func (e *Emitter) retryLoop(ctx context.Context) {
	tick := time.NewTicker(retryTick)
	defer tick.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		e.moveDue()
		if time.Since(lastPrune) >= pruneEvery {
			lastPrune = time.Now()
			if err := e.Hooks.Prune(ctx, time.Now().Add(-deliveryRetention)); err != nil {
				logger.Error("apikit: webhook delivery prune failed err=%v", err)
			}
		}
	}
}

func (e *Emitter) moveDue() {
	client := redis.Client()
	if client == nil {
		return
	}
	key := redis.Key(e.retryKey())
	due, err := client.ZRangeByScore(redis.Ctx, key, &goredis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(time.Now().Unix(), 10), Count: 100}).Result()
	if err != nil {
		logger.Persistent("apikit.webhook.retry", 3, 10*time.Minute, "apikit: webhook retry scan failed err=%v", err)
		return
	}
	logger.ResetPersistent("apikit.webhook.retry")
	for _, member := range due {
		if err := redis.Enqueue(e.queue(), json.RawMessage(member)); err != nil {
			logger.Error("apikit: webhook retry requeue failed err=%v", err)
			continue
		}
		if err := client.ZRem(redis.Ctx, key, member).Err(); err != nil {
			logger.Error("apikit: webhook retry dequeue-from-schedule failed err=%v", err)
		}
	}
}

func (e *Emitter) SendTest(ctx context.Context, hook *Webhook) (Delivery, error) {
	secret, err := e.Open(hook.SecretEnc)
	if err != nil {
		return Delivery{}, err
	}
	id, err := NewEventID()
	if err != nil {
		return Delivery{}, err
	}
	body, err := json.Marshal(Event{ID: id, Type: "webhook.test", CreatedAt: time.Now().UTC(), Data: map[string]string{"message": "test event"}})
	if err != nil {
		return Delivery{}, err
	}
	res := Send(ctx, e.client(), hook.URL, secret, id, "webhook.test", body)
	d := Delivery{
		WebhookID: hook.ID, EventID: id, EventType: "webhook.test", Attempt: 1,
		StatusCode: res.StatusCode, OK: res.OK, Error: res.Error, DurationMS: int(res.Duration / time.Millisecond), CreatedAt: time.Now().UTC(),
	}
	e.record(ctx, d)
	return d, nil
}
