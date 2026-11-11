package apikit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Webhook struct {
	ID        int64
	OwnerID   int64
	URL       string
	SecretEnc string
	Events    []string
	Enabled   bool
	FailCount int
	CreatedAt time.Time
}

func (w *Webhook) Wants(eventType string) bool {
	for _, e := range w.Events {
		if e == "*" || e == eventType {
			return true
		}
	}
	return false
}

type Delivery struct {
	ID         int64
	WebhookID  int64
	EventID    string
	EventType  string
	Attempt    int
	StatusCode int
	OK         bool
	Error      string
	DurationMS int
	CreatedAt  time.Time
}

type WebhookStore interface {
	Create(ctx context.Context, w Webhook) (int64, error)
	Get(ctx context.Context, id int64) (*Webhook, error)
	List(ctx context.Context, owner int64) ([]Webhook, error)
	ListFor(ctx context.Context, owner int64, eventType string) ([]Webhook, error)
	Delete(ctx context.Context, id, owner int64) (bool, error)
	SetEnabled(ctx context.Context, id, owner int64, enabled bool) (bool, error)
	RecordResult(ctx context.Context, id int64, ok bool, disableAfter int) (bool, error)
	AddDelivery(ctx context.Context, d Delivery) error
	Deliveries(ctx context.Context, webhookID, owner int64, limit int) ([]Delivery, error)
	Prune(ctx context.Context, olderThan time.Time) error
}

type SQLWebhookStore struct {
	DB *sql.DB
}

const hookColumns = `id, owner_id, url, secret_enc, events, enabled, fail_count, created_at`

func scanHook(sc rowScanner) (*Webhook, error) {
	var (
		w       Webhook
		events  string
		enabled int
		created any
	)
	if err := sc.Scan(&w.ID, &w.OwnerID, &w.URL, &w.SecretEnc, &events, &enabled, &w.FailCount, &created); err != nil {
		return nil, err
	}
	w.Events = splitScopes(events)
	w.Enabled = enabled != 0
	t, err := parseDBTime(created)
	if err != nil {
		return nil, err
	}
	if t != nil {
		w.CreatedAt = *t
	}
	return &w, nil
}

func (s *SQLWebhookStore) Create(ctx context.Context, w Webhook) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_webhooks (owner_id, url, secret_enc, events, enabled, fail_count, created_at) VALUES (?, ?, ?, ?, 1, 0, ?)`,
		w.OwnerID, w.URL, w.SecretEnc, strings.Join(w.Events, ","), formatTime(time.Now()))
	if err != nil {
		return 0, fmt.Errorf("apikit: insert webhook owner=%d url=%q: %w", w.OwnerID, w.URL, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("apikit: read webhook id owner=%d: %w", w.OwnerID, err)
	}
	return id, nil
}

func (s *SQLWebhookStore) Get(ctx context.Context, id int64) (*Webhook, error) {
	w, err := scanHook(s.DB.QueryRowContext(ctx, `SELECT `+hookColumns+` FROM api_webhooks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apikit: get webhook id=%d: %w", id, err)
	}
	return w, nil
}

func (s *SQLWebhookStore) query(ctx context.Context, q string, args ...any) ([]Webhook, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("apikit: query webhooks: %w", err)
	}
	defer rows.Close()
	var out []Webhook
	for rows.Next() {
		w, err := scanHook(rows)
		if err != nil {
			return nil, fmt.Errorf("apikit: scan webhook: %w", err)
		}
		out = append(out, *w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apikit: iterate webhooks: %w", err)
	}
	return out, nil
}

func (s *SQLWebhookStore) List(ctx context.Context, owner int64) ([]Webhook, error) {
	if owner != 0 {
		return s.query(ctx, `SELECT `+hookColumns+` FROM api_webhooks WHERE owner_id = ? ORDER BY id DESC`, owner)
	}
	return s.query(ctx, `SELECT `+hookColumns+` FROM api_webhooks ORDER BY id DESC`)
}

func (s *SQLWebhookStore) ListFor(ctx context.Context, owner int64, eventType string) ([]Webhook, error) {
	var all []Webhook
	var err error
	if owner != 0 {
		all, err = s.query(ctx, `SELECT `+hookColumns+` FROM api_webhooks WHERE enabled = 1 AND owner_id = ?`, owner)
	} else {
		all, err = s.query(ctx, `SELECT `+hookColumns+` FROM api_webhooks WHERE enabled = 1`)
	}
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, w := range all {
		if w.Wants(eventType) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (s *SQLWebhookStore) Delete(ctx context.Context, id, owner int64) (bool, error) {
	q := `DELETE FROM api_webhooks WHERE id = ?`
	args := []any{id}
	if owner != 0 {
		q += ` AND owner_id = ?`
		args = append(args, owner)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("apikit: delete webhook id=%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("apikit: delete webhook id=%d rows: %w", id, err)
	}
	if n > 0 {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM api_webhook_deliveries WHERE webhook_id = ?`, id); err != nil {
			return true, fmt.Errorf("apikit: delete deliveries webhook id=%d: %w", id, err)
		}
	}
	return n > 0, nil
}

func (s *SQLWebhookStore) SetEnabled(ctx context.Context, id, owner int64, enabled bool) (bool, error) {
	v := 0
	if enabled {
		v = 1
	}
	q := `UPDATE api_webhooks SET enabled = ?, fail_count = 0 WHERE id = ?`
	args := []any{v, id}
	if owner != 0 {
		q += ` AND owner_id = ?`
		args = append(args, owner)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("apikit: set webhook enabled id=%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("apikit: set webhook enabled id=%d rows: %w", id, err)
	}
	return n > 0, nil
}

func (s *SQLWebhookStore) RecordResult(ctx context.Context, id int64, ok bool, disableAfter int) (bool, error) {
	if ok {
		if _, err := s.DB.ExecContext(ctx, `UPDATE api_webhooks SET fail_count = 0 WHERE id = ? AND fail_count <> 0`, id); err != nil {
			return false, fmt.Errorf("apikit: reset webhook failures id=%d: %w", id, err)
		}
		return false, nil
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE api_webhooks SET fail_count = fail_count + 1 WHERE id = ?`, id); err != nil {
		return false, fmt.Errorf("apikit: count webhook failure id=%d: %w", id, err)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE api_webhooks SET enabled = 0 WHERE id = ? AND enabled = 1 AND fail_count >= ?`, id, disableAfter)
	if err != nil {
		return false, fmt.Errorf("apikit: auto-disable webhook id=%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("apikit: auto-disable webhook id=%d rows: %w", id, err)
	}
	return n > 0, nil
}

func (s *SQLWebhookStore) AddDelivery(ctx context.Context, d Delivery) error {
	ok := 0
	if d.OK {
		ok = 1
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO api_webhook_deliveries (webhook_id, event_id, event_type, attempt, status_code, ok, error, duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.WebhookID, d.EventID, d.EventType, d.Attempt, d.StatusCode, ok, truncate(d.Error, 400), d.DurationMS, formatTime(time.Now())); err != nil {
		return fmt.Errorf("apikit: record delivery webhook=%d event=%s: %w", d.WebhookID, d.EventID, err)
	}
	return nil
}

func (s *SQLWebhookStore) Deliveries(ctx context.Context, webhookID, owner int64, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT d.id, d.webhook_id, d.event_id, d.event_type, d.attempt, d.status_code, d.ok, d.error, d.duration_ms, d.created_at
FROM api_webhook_deliveries d JOIN api_webhooks w ON w.id = d.webhook_id WHERE d.webhook_id = ?`
	args := []any{webhookID}
	if owner != 0 {
		q += ` AND w.owner_id = ?`
		args = append(args, owner)
	}
	q += ` ORDER BY d.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("apikit: list deliveries webhook=%d: %w", webhookID, err)
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var (
			d       Delivery
			ok      int
			created any
		)
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventID, &d.EventType, &d.Attempt, &d.StatusCode, &ok, &d.Error, &d.DurationMS, &created); err != nil {
			return nil, fmt.Errorf("apikit: scan delivery: %w", err)
		}
		d.OK = ok != 0
		if t, err := parseDBTime(created); err != nil {
			return nil, err
		} else if t != nil {
			d.CreatedAt = *t
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apikit: iterate deliveries: %w", err)
	}
	return out, nil
}

func (s *SQLWebhookStore) Prune(ctx context.Context, olderThan time.Time) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM api_webhook_deliveries WHERE created_at < ?`, formatTime(olderThan)); err != nil {
		return fmt.Errorf("apikit: prune deliveries: %w", err)
	}
	return nil
}
