package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

var ErrQueueFull = errors.New("telegram: send queue for this bot is full")

const (
	queueDepth       = 512
	queueJobTimeout  = 30 * time.Second
	queueIdleExit    = 2 * time.Minute
	queueMaxAttempts = 4
	registryMax      = 2000
	registryIdle     = 10 * time.Minute
)

var retryDelays = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

type Job struct {
	Op   string
	Run  func(ctx context.Context, b *Bot) error
	Done func(err error)
}

type item struct {
	job       Job
	attempts  int
	notBefore time.Time
}

type tokenQueue struct {
	mu      sync.Mutex
	items   []*item
	wake    chan struct{}
	running bool
	bot     *Bot
	used    time.Time
}

var (
	regMu sync.Mutex
	reg   = map[string]*tokenQueue{}
)

func queueFor(token string) *tokenQueue {
	token = strings.TrimSpace(token)
	regMu.Lock()
	defer regMu.Unlock()
	if q, ok := reg[token]; ok {
		q.used = time.Now()
		return q
	}
	if len(reg) >= registryMax {
		cut := time.Now().Add(-registryIdle)
		for k, q := range reg {
			q.mu.Lock()
			idle := !q.running && len(q.items) == 0 && q.used.Before(cut)
			q.mu.Unlock()
			if idle {
				delete(reg, k)
			}
		}
	}
	q := &tokenQueue{wake: make(chan struct{}, 1), bot: New(token, "", ""), used: time.Now()}
	reg[token] = q
	return q
}

func For(token string) *Bot {
	return queueFor(token).bot
}

func Submit(token string, job Job) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrNotConfigured
	}
	if job.Run == nil {
		return errors.New("telegram: job has no Run function")
	}
	q := queueFor(token)
	q.mu.Lock()
	if len(q.items) >= queueDepth {
		q.mu.Unlock()
		logger.Warn("telegram queue full bot=%s op=%s depth=%d", botLabel(token), job.Op, queueDepth)
		return ErrQueueFull
	}
	q.items = append(q.items, &item{job: job})
	start := !q.running
	if start {
		q.running = true
	}
	q.mu.Unlock()
	if start {
		go q.loop(token)
	} else {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func Do(ctx context.Context, token, op string, run func(ctx context.Context, b *Bot) error) error {
	res := make(chan error, 1)
	if err := Submit(token, Job{Op: op, Run: run, Done: func(err error) { res <- err }}); err != nil {
		return err
	}
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func botLabel(token string) string {
	if i := strings.IndexByte(token, ':'); i > 0 {
		return token[:i]
	}
	return "unknown"
}

func (q *tokenQueue) next() (it *item, wait time.Duration, empty bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil, 0, true
	}
	now := time.Now()
	var soonest time.Time
	for i, c := range q.items {
		if !c.notBefore.After(now) {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return c, 0, false
		}
		if soonest.IsZero() || c.notBefore.Before(soonest) {
			soonest = c.notBefore
		}
	}
	return nil, time.Until(soonest), false
}

func (q *tokenQueue) loop(token string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("telegram queue worker panic bot=%s: %v", botLabel(token), r)
			logger.SentryRecover(r)
			q.mu.Lock()
			q.running = len(q.items) > 0
			restart := q.running
			q.mu.Unlock()
			if restart {
				go q.loop(token)
			}
		}
	}()
	for {
		it, wait, empty := q.next()
		if it == nil {
			idle := wait
			if empty {
				idle = queueIdleExit
			}
			t := time.NewTimer(idle)
			select {
			case <-q.wake:
				t.Stop()
				continue
			case <-t.C:
			}
			if empty {
				q.mu.Lock()
				if len(q.items) == 0 {
					q.running = false
					q.mu.Unlock()
					return
				}
				q.mu.Unlock()
			}
			continue
		}
		q.run(token, it)
	}
}

func (q *tokenQueue) run(token string, it *item) {
	ctx, cancel := context.WithTimeout(context.Background(), queueJobTimeout)
	err := it.job.Run(ctx, q.bot)
	cancel()
	if err != nil && IsTransient(err) && it.attempts+1 < queueMaxAttempts {
		delay := retryDelays[min(it.attempts, len(retryDelays)-1)]
		var rl rateLimitedError
		if errors.As(err, &rl) && rl.wait > delay {
			delay = rl.wait
		}
		it.attempts++
		it.notBefore = time.Now().Add(delay)
		logger.Warn("telegram send will retry bot=%s op=%s attempt=%d in %s err=%v", botLabel(token), it.job.Op, it.attempts, delay, err)
		q.mu.Lock()
		q.items = append(q.items, it)
		q.mu.Unlock()
		return
	}
	if err != nil {
		logger.Warn("telegram send failed bot=%s op=%s attempts=%d err=%v", botLabel(token), it.job.Op, it.attempts+1, err)
	}
	if it.job.Done != nil {
		it.job.Done(err)
	}
}
