package emailchange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SQLStore keeps the pending change in columns of the users table. Identifier
// fields are fixed strings from app code, never user input. TextTime stores
// the expiry as a UTC "2006-01-02 15:04:05" string, which SQLite needs; MySQL
// leaves it false and gets a time.Time.
type SQLStore struct {
	DB         *sql.DB
	Table      string
	IDCol      string
	EmailCol   string
	PendingCol string
	TokenCol   string
	ExpiresCol string
	ApplyExtra string
	TextTime   bool
	// EmptyNotNull clears the pending columns to '' instead of NULL, for
	// tables that declare them NOT NULL DEFAULT ''.
	EmptyNotNull bool
}

func (s *SQLStore) nul() string {
	if s.EmptyNotNull {
		return "''"
	}
	return "NULL"
}

func (s *SQLStore) t() string  { return pick(s.Table, "users") }
func (s *SQLStore) id() string { return pick(s.IDCol, "id") }
func (s *SQLStore) em() string { return pick(s.EmailCol, "email") }
func (s *SQLStore) pe() string { return pick(s.PendingCol, "pending_email") }
func (s *SQLStore) tk() string { return pick(s.TokenCol, "pending_email_token") }
func (s *SQLStore) ex() string { return pick(s.ExpiresCol, "pending_email_expires") }

func pick(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func (s *SQLStore) expiry(t time.Time) any {
	if s.TextTime {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return t
}

func (s *SQLStore) SetPending(ctx context.Context, userID int64, newEmail, tokenHash string, expires time.Time) error {
	q := fmt.Sprintf("UPDATE %s SET %s = ?, %s = ?, %s = ? WHERE %s = ?", s.t(), s.pe(), s.tk(), s.ex(), s.id())
	res, err := s.DB.ExecContext(ctx, q, newEmail, tokenHash, s.expiry(expires), userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

var textLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

func parseExpiry(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case []byte:
		return parseExpiry(string(x))
	case string:
		for _, l := range textLayouts {
			if t, err := time.ParseInLocation(l, x, time.UTC); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("emailchange: unreadable expiry %q", x)
	case nil:
		return time.Time{}, errors.New("emailchange: empty expiry")
	}
	return time.Time{}, fmt.Errorf("emailchange: unsupported expiry type %T", v)
}

func (s *SQLStore) FindPending(ctx context.Context, tokenHash string) (Pending, error) {
	q := fmt.Sprintf("SELECT %s, %s, %s FROM %s WHERE %s = ?", s.id(), s.pe(), s.ex(), s.t(), s.tk())
	var p Pending
	var pending sql.NullString
	var exp any
	err := s.DB.QueryRowContext(ctx, q, tokenHash).Scan(&p.UserID, &pending, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, ErrNotFound
	}
	if err != nil {
		return Pending{}, err
	}
	if !pending.Valid || pending.String == "" {
		return Pending{}, ErrNotFound
	}
	p.NewEmail = pending.String
	if p.Expires, err = parseExpiry(exp); err != nil {
		return Pending{}, err
	}
	return p, nil
}

func (s *SQLStore) EmailHolder(ctx context.Context, email string, exceptUserID int64) (int64, error) {
	q := fmt.Sprintf("SELECT %s FROM %s WHERE LOWER(%s) = LOWER(?) AND %s <> ? LIMIT 1", s.id(), s.t(), s.em(), s.id())
	var id int64
	err := s.DB.QueryRowContext(ctx, q, email, exceptUserID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *SQLStore) Apply(ctx context.Context, userID int64, newEmail string) error {
	extra := ""
	if s.ApplyExtra != "" {
		extra = ", " + s.ApplyExtra
	}
	q := fmt.Sprintf("UPDATE %s SET %s = ?, %s = %s, %s = %s, %s = %s%s WHERE %s = ? AND %s = ?",
		s.t(), s.em(), s.pe(), s.nul(), s.tk(), s.nul(), s.ex(), s.nul(), extra, s.id(), s.pe())
	res, err := s.DB.ExecContext(ctx, q, newEmail, userID, newEmail)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) ClearPending(ctx context.Context, userID int64) error {
	q := fmt.Sprintf("UPDATE %s SET %s = %s, %s = %s, %s = %s WHERE %s = ?", s.t(), s.pe(), s.nul(), s.tk(), s.nul(), s.ex(), s.nul(), s.id())
	_, err := s.DB.ExecContext(ctx, q, userID)
	return err
}
