package notifykit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SQLStore keeps the bell's notifications, each person's channel choices and
// the once-only markers in three tables, on SQLite or MySQL.
type SQLStore struct {
	db      *sql.DB
	mysql   bool
	pending string
}

// NewSQLStore wraps a database. dialect is "sqlite" or "mysql". Create the
// tables with Schema(dialect) in the product's migrations.
func NewSQLStore(db *sql.DB, dialect string) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("notifykit: db is required")
	}
	if dialect != "sqlite" && dialect != "mysql" {
		return nil, fmt.Errorf("notifykit: unknown dialect %q", dialect)
	}
	return &SQLStore{db: db, mysql: dialect == "mysql"}, nil
}

// Schema returns the table definitions for a dialect.
func Schema(dialect string) string {
	if dialect == "mysql" {
		return `CREATE TABLE IF NOT EXISTS notifications (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  event VARCHAR(100) NOT NULL,
  category VARCHAR(40) NOT NULL,
  title VARCHAR(255) NOT NULL,
  body TEXT,
  link VARCHAR(500) NOT NULL DEFAULT '',
  is_read TINYINT NOT NULL DEFAULT 0,
  extra TEXT,
  created_at BIGINT NOT NULL,
  KEY idx_notif_user (user_id, is_read, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS notification_prefs (
  user_id BIGINT NOT NULL,
  category VARCHAR(40) NOT NULL,
  channel VARCHAR(20) NOT NULL,
  enabled TINYINT NOT NULL,
  PRIMARY KEY (user_id, category, channel)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE IF NOT EXISTS notification_sent (
  user_id BIGINT NOT NULL,
  event VARCHAR(100) NOT NULL,
  dedupe_key VARCHAR(191) NOT NULL,
  sent_at BIGINT NOT NULL,
  PRIMARY KEY (user_id, event, dedupe_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`
	}
	return `CREATE TABLE IF NOT EXISTS notifications(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	event TEXT NOT NULL,
	category TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	link TEXT NOT NULL DEFAULT '',
	is_read INTEGER NOT NULL DEFAULT 0,
	extra TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notif_user ON notifications(user_id, is_read, created_at);
CREATE TABLE IF NOT EXISTS notification_prefs(
	user_id INTEGER NOT NULL,
	category TEXT NOT NULL,
	channel TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	PRIMARY KEY(user_id, category, channel)
);
CREATE TABLE IF NOT EXISTS notification_sent(
	user_id INTEGER NOT NULL,
	event TEXT NOT NULL,
	dedupe_key TEXT NOT NULL,
	sent_at INTEGER NOT NULL,
	PRIMARY KEY(user_id, event, dedupe_key)
);`
}

// Add stores one in-app notification.
func (s *SQLStore) Add(userID int64, it Item) error {
	extra, err := json.Marshal(it.Extra)
	if err != nil || it.Extra == nil {
		extra = []byte("{}")
	}
	_, err = s.db.Exec(`INSERT INTO notifications(user_id,event,category,title,body,link,is_read,extra,created_at) VALUES(?,?,?,?,?,?,0,?,?)`,
		userID, it.Event, NormalizeCategory(it.Category), it.Title, it.Body, it.Link, string(extra), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("store notification %s for user %d: %w", it.Event, userID, err)
	}
	return nil
}

// Unread counts a person's unread notifications.
func (s *SQLStore) Unread(userID int64) (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=? AND is_read=0`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread for user %d: %w", userID, err)
	}
	return n, nil
}

func (s *SQLStore) Counts(userID int64) ([]CategoryCount, error) {
	rows, err := s.db.Query(`SELECT category, SUM(CASE WHEN is_read=0 THEN 1 ELSE 0 END), COUNT(*), MAX(created_at) FROM notifications WHERE user_id=? GROUP BY category`, userID)
	if err != nil {
		return nil, fmt.Errorf("notification counts for user %d: %w", userID, err)
	}
	var out []CategoryCount
	for rows.Next() {
		var c CategoryCount
		var latest int64
		if err := rows.Scan(&c.Category, &c.Unread, &c.Total, &latest); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan notification counts: %w", err)
		}
		c.LatestAt = time.Unix(latest, 0).UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err := s.db.QueryRow(`SELECT title FROM notifications WHERE user_id=? AND category=? ORDER BY created_at DESC, id DESC LIMIT 1`, userID, out[i].Category).Scan(&out[i].LatestTitle); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("latest notification title for user %d: %w", userID, err)
		}
	}
	return SortCounts(out), nil
}

func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var read int
	var created int64
	var extra string
	if err := row.Scan(&it.ID, &it.Event, &it.Category, &it.Title, &it.Body, &it.Link, &read, &extra, &created); err != nil {
		return Item{}, err
	}
	it.Read = read == 1
	it.CreatedAt = time.Unix(created, 0).UTC()
	if extra != "" && extra != "{}" {
		_ = json.Unmarshal([]byte(extra), &it.Extra)
	}
	return it, nil
}

const itemCols = `id,event,category,title,body,link,is_read,extra,created_at`

func (s *SQLStore) List(userID int64, f Filter) ([]Item, int, error) {
	where := `user_id=?`
	args := []any{userID}
	if f.Category != "" {
		where += ` AND category=?`
		args = append(args, f.Category)
	}
	if f.UnreadOnly {
		where += ` AND is_read=0`
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count notifications for user %d: %w", userID, err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = PageSize
	}
	rows, err := s.db.Query(`SELECT `+itemCols+` FROM notifications WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications for user %d: %w", userID, err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

func (s *SQLStore) Get(userID, id int64) (Item, error) {
	it, err := scanItem(s.db.QueryRow(`SELECT `+itemCols+` FROM notifications WHERE user_id=? AND id=?`, userID, id))
	if err != nil {
		return Item{}, fmt.Errorf("notification %d for user %d: %w", id, userID, err)
	}
	return it, nil
}

func (s *SQLStore) MarkRead(userID, id int64) error {
	if _, err := s.db.Exec(`UPDATE notifications SET is_read=1 WHERE user_id=? AND id=?`, userID, id); err != nil {
		return fmt.Errorf("mark notification %d read for user %d: %w", id, userID, err)
	}
	return nil
}

func (s *SQLStore) MarkCategoryRead(userID int64, category string) (int64, error) {
	res, err := s.db.Exec(`UPDATE notifications SET is_read=1 WHERE user_id=? AND category=? AND is_read=0`, userID, category)
	if err != nil {
		return 0, fmt.Errorf("mark category %s read for user %d: %w", category, userID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Pref reads a stored choice; set is false when the person never chose.
func (s *SQLStore) Pref(userID int64, category, channel string) (enabled, set bool, err error) {
	var v int
	err = s.db.QueryRow(`SELECT enabled FROM notification_prefs WHERE user_id=? AND category=? AND channel=?`, userID, category, channel).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read preference %s/%s for user %d: %w", category, channel, userID, err)
	}
	return v == 1, true, nil
}

// Prefs returns every stored choice of a person as "category/channel" keys.
func (s *SQLStore) Prefs(userID int64) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT category, channel, enabled FROM notification_prefs WHERE user_id=?`, userID)
	if err != nil {
		return nil, fmt.Errorf("read preferences for user %d: %w", userID, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c, ch string
		var v int
		if err := rows.Scan(&c, &ch, &v); err != nil {
			return nil, fmt.Errorf("scan preference: %w", err)
		}
		out[c+"/"+ch] = v == 1
	}
	return out, rows.Err()
}

// SetPrefs replaces a person's choices for the given "category/channel" keys.
func (s *SQLStore) SetPrefs(userID int64, values map[string]bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("save preferences for user %d: %w", userID, err)
	}
	defer tx.Rollback()
	upsert := `INSERT OR REPLACE INTO notification_prefs(user_id,category,channel,enabled) VALUES(?,?,?,?)`
	if s.mysql {
		upsert = `REPLACE INTO notification_prefs(user_id,category,channel,enabled) VALUES(?,?,?,?)`
	}
	for key, on := range values {
		cat, ch, ok := strings.Cut(key, "/")
		if !ok {
			continue
		}
		v := 0
		if on {
			v = 1
		}
		if _, err := tx.Exec(upsert, userID, cat, ch, v); err != nil {
			return fmt.Errorf("save preference %s for user %d: %w", key, userID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save preferences for user %d: commit: %w", userID, err)
	}
	return nil
}

// Once records a (user, event, key) and reports whether it was new.
func (s *SQLStore) Once(userID int64, event, key string) (bool, error) {
	q := `INSERT OR IGNORE INTO notification_sent(user_id,event,dedupe_key,sent_at) VALUES(?,?,?,?)`
	if s.mysql {
		q = `INSERT IGNORE INTO notification_sent(user_id,event,dedupe_key,sent_at) VALUES(?,?,?,?)`
	}
	res, err := s.db.Exec(q, userID, event, key, time.Now().Unix())
	if err != nil {
		return false, fmt.Errorf("record %s/%s for user %d: %w", event, key, userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Forget releases a once-only marker after a failed send so it can retry.
func (s *SQLStore) Forget(userID int64, event, key string) error {
	if _, err := s.db.Exec(`DELETE FROM notification_sent WHERE user_id=? AND event=? AND dedupe_key=?`, userID, event, key); err != nil {
		return fmt.Errorf("release %s/%s for user %d: %w", event, key, userID, err)
	}
	return nil
}

// Prune deletes read notifications older than the cutoff.
func (s *SQLStore) Prune(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM notifications WHERE is_read=1 AND created_at<?`, olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune notifications: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
