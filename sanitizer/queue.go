package sanitizer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

// Queue feeds video jobs to a Backend through Redis so that uploads return at
// once, a backlog survives restarts, and no more than MaxInFlight jobs run at a
// time. Redis holds only the job id and its retry state; the signed URLs are
// minted by Resolve at the moment of dispatch, so they never sit in Redis and
// never expire while a job waits its turn.
type Queue struct {
	Name        string
	Backend     func() Backend
	Resolve     func(ctx context.Context, id string) (Job, error)
	OnGiveUp    func(id string, err error)
	MaxInFlight int
	MaxAttempts int
	Backoff     time.Duration
	JobTTL      time.Duration
}

type queueEntry struct {
	ID        string `json:"id"`
	Attempts  int    `json:"attempts"`
	NotBefore int64  `json:"not_before"`
}

func (q *Queue) defaults() {
	if q.MaxInFlight <= 0 {
		q.MaxInFlight = 1
	}
	if q.MaxAttempts <= 0 {
		q.MaxAttempts = 5
	}
	if q.Backoff <= 0 {
		q.Backoff = 30 * time.Second
	}
	if q.JobTTL <= 0 {
		q.JobTTL = 6 * time.Hour
	}
}

func (q *Queue) inflightKey() string { return redis.Key(q.Name + ":inflight") }

// Enqueue schedules the job with the given id.
func (q *Queue) Enqueue(id string) error {
	if !jobIDRe.MatchString(id) {
		return fmt.Errorf("invalid job id %q", id)
	}
	if err := redis.Enqueue(q.Name, queueEntry{ID: id}); err != nil {
		logger.Error("video queue: enqueue of job %s failed: %v", id, err)
		return fmt.Errorf("enqueue video job %s: %w", id, err)
	}
	return nil
}

// Done frees the job's slot. Call it when the worker reports an outcome or the
// job is abandoned.
func (q *Queue) Done(id string) {
	c := redis.Client()
	if c == nil {
		return
	}
	if err := c.ZRem(redis.Ctx, q.inflightKey(), id).Err(); err != nil {
		logger.Error("video queue: freeing the slot of job %s failed (it frees itself after %s): %v", id, q.JobTTL, err)
	}
}

func (q *Queue) inflight() (int64, error) {
	c := redis.Client()
	if c == nil {
		return 0, fmt.Errorf("redis is not connected")
	}
	now := time.Now().Unix()
	if err := c.ZRemRangeByScore(redis.Ctx, q.inflightKey(), "-inf", fmt.Sprint(now)).Err(); err != nil {
		return 0, err
	}
	return c.ZCard(redis.Ctx, q.inflightKey()).Result()
}

// Run dispatches queued jobs until ctx ends.
func (q *Queue) Run(ctx context.Context) {
	q.defaults()
	for ctx.Err() == nil {
		n, err := q.inflight()
		if err != nil {
			logger.Warn("video queue: cannot read the in-flight count, retrying: %v", err)
			sleepCtx(ctx, 15*time.Second)
			continue
		}
		if n >= int64(q.MaxInFlight) {
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		raw, ok, err := redis.Dequeue(q.Name, 5*time.Second)
		if err != nil {
			logger.Warn("video queue: dequeue failed, retrying: %v", err)
			sleepCtx(ctx, 10*time.Second)
			continue
		}
		if !ok {
			continue
		}
		var e queueEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			logger.Error("video queue: dropping an unreadable entry %q: %v", string(raw), err)
			continue
		}
		q.dispatch(ctx, e)
	}
}

func (q *Queue) dispatch(ctx context.Context, e queueEntry) {
	if e.NotBefore > time.Now().Unix() {
		if err := redis.Enqueue(q.Name, e); err != nil {
			logger.Error("video queue: could not requeue the delayed job %s: %v", e.ID, err)
			q.giveUp(e, err)
			return
		}
		sleepCtx(ctx, 2*time.Second)
		return
	}
	c := redis.Client()
	if c != nil {
		if err := c.ZAdd(redis.Ctx, q.inflightKey(), goredis.Z{Score: float64(time.Now().Add(q.JobTTL).Unix()), Member: e.ID}).Err(); err != nil {
			logger.Error("video queue: could not mark job %s in flight: %v", e.ID, err)
		}
	}
	job, err := q.Resolve(ctx, e.ID)
	if err == nil {
		b := q.Backend()
		if err = b.Submit(ctx, job); err != nil {
			err = fmt.Errorf("submit to the %s backend: %w", b.Name(), err)
		}
	}
	if err == nil {
		logger.Info("video queue: job %s dispatched", e.ID)
		return
	}
	q.Done(e.ID)
	e.Attempts++
	if e.Attempts >= q.MaxAttempts {
		logger.Error("video queue: job %s failed after %d attempts: %v", e.ID, e.Attempts, err)
		q.giveUp(e, err)
		return
	}
	delay := q.Backoff * time.Duration(1<<uint(e.Attempts-1))
	e.NotBefore = time.Now().Add(delay).Unix()
	logger.Warn("video queue: job %s failed (attempt %d of %d), retrying in %s: %v", e.ID, e.Attempts, q.MaxAttempts, delay, err)
	if rerr := redis.Enqueue(q.Name, e); rerr != nil {
		logger.Error("video queue: could not requeue job %s: %v", e.ID, rerr)
		q.giveUp(e, rerr)
	}
}

func (q *Queue) giveUp(e queueEntry, err error) {
	if q.OnGiveUp != nil {
		q.OnGiveUp(e.ID, err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
