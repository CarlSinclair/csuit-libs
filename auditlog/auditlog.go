// Package auditlog is the one audit trail every product uses: who did what,
// to which record, from where. It stores no IP addresses; location comes from
// geo.RequestLocation. The table layout is configurable so products with an
// older table keep it, and every column except the action may be absent.
package auditlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/geo"
	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusError = "error"

	absent = "-"
)

// Columns names the columns of the audit table. An empty name takes the
// standard name; "-" means the table has no such column.
type Columns struct {
	ID, UserID, Action, ResourceType, EntityID, Status, Details, Location, UserAgent, ScopeID, CreatedAt string
}

func (c Columns) withDefaults() Columns {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&c.ID, "id")
	def(&c.UserID, "user_id")
	def(&c.Action, "action")
	def(&c.ResourceType, "resource_type")
	def(&c.EntityID, "entity_id")
	def(&c.Status, "status")
	def(&c.Details, "details")
	def(&c.Location, "location")
	def(&c.UserAgent, "user_agent")
	def(&c.ScopeID, "-")
	def(&c.CreatedAt, "created_at")
	return c
}

func has(col string) bool { return col != "" && col != absent }

// Config describes where the audit trail lives.
type Config struct {
	DB    *sql.DB
	Table string
	Cols  Columns
	// TimeUnix stores created_at as unix seconds. Otherwise the column is a
	// DATETIME that the database fills in on insert.
	TimeUnix bool
	// UsersTable, UsersIDCol and UsersLabelExpr (an expression over alias u)
	// let the list show and search user names. All optional.
	UsersTable     string
	UsersIDCol     string
	UsersLabelExpr string
	// Silent stops Record from writing its own log line, for products that
	// already log the action themselves.
	Silent bool
}

type Log struct {
	cfg Config
	c   Columns
}

func New(cfg Config) (*Log, error) {
	if cfg.DB == nil {
		return nil, errors.New("auditlog: DB is required")
	}
	if cfg.Table == "" {
		cfg.Table = "audit_logs"
	}
	if !safeIdent(cfg.Table) {
		return nil, fmt.Errorf("auditlog: unsafe table name %q", cfg.Table)
	}
	c := cfg.Cols.withDefaults()
	for _, v := range []string{c.ID, c.UserID, c.Action, c.ResourceType, c.EntityID, c.Status, c.Details, c.Location, c.UserAgent, c.ScopeID, c.CreatedAt} {
		if v != absent && !safeIdent(v) {
			return nil, fmt.Errorf("auditlog: unsafe column name %q", v)
		}
	}
	if !has(c.Action) {
		return nil, errors.New("auditlog: the action column is required")
	}
	if cfg.UsersTable != "" {
		if !safeIdent(cfg.UsersTable) || !safeIdent(cfg.UsersIDCol) || cfg.UsersLabelExpr == "" {
			return nil, errors.New("auditlog: UsersTable needs UsersIDCol and UsersLabelExpr")
		}
	}
	return &Log{cfg: cfg, c: c}, nil
}

func safeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// Entry is one audit record.
type Entry struct {
	ID           int64
	UserID       int64
	UserLabel    string
	Action       string
	ResourceType string
	EntityID     string
	Status       string
	ScopeID      int64
	Details      map[string]string
	RawDetails   string
	Location     string
	UserAgent    string
	At           time.Time
	// Extra holds values for product-specific columns of the table (for
	// example a hashed IP); the names must be plain identifiers.
	Extra map[string]any
}

// ResourceOf derives the resource type from an action such as "course_created".
func ResourceOf(action string) string {
	if i := strings.IndexByte(action, '_'); i > 0 {
		return action[:i]
	}
	return action
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Write stores an entry. A missing resource type is derived from the action.
func (l *Log) Write(ctx context.Context, e Entry) error {
	_, err := l.WriteID(ctx, e)
	return err
}

// WriteID is Write that also returns the new row id (0 when the driver cannot
// report it).
func (l *Log) WriteID(ctx context.Context, e Entry) (int64, error) {
	if strings.TrimSpace(e.Action) == "" {
		return 0, errors.New("auditlog: empty action")
	}
	if e.ResourceType == "" {
		e.ResourceType = ResourceOf(e.Action)
	}
	if e.Status == "" {
		e.Status = StatusOK
	}
	raw := e.RawDetails
	if raw == "" {
		b, err := json.Marshal(e.Details)
		if err != nil {
			logger.Error("auditlog: details of %q for user %d not encoded: %v", e.Action, e.UserID, err)
			b = []byte("{}")
		}
		raw = string(b)
	}
	var cols []string
	var args []any
	add := func(col string, v any) {
		if has(col) {
			cols = append(cols, col)
			args = append(args, v)
		}
	}
	add(l.c.UserID, e.UserID)
	add(l.c.Action, e.Action)
	add(l.c.ResourceType, e.ResourceType)
	add(l.c.EntityID, clip(e.EntityID, 191))
	add(l.c.Status, e.Status)
	add(l.c.Details, raw)
	add(l.c.Location, clip(e.Location, 191))
	add(l.c.UserAgent, clip(e.UserAgent, 200))
	if e.ScopeID != 0 {
		add(l.c.ScopeID, e.ScopeID)
	} else if has(l.c.ScopeID) {
		add(l.c.ScopeID, nil)
	}
	for k, v := range e.Extra {
		if safeIdent(k) {
			add(k, v)
		}
	}
	if l.cfg.TimeUnix && has(l.c.CreatedAt) {
		add(l.c.CreatedAt, time.Now().Unix())
	}
	q := "INSERT INTO " + l.cfg.Table + "(" + strings.Join(cols, ",") + ") VALUES(?" + strings.Repeat(",?", len(cols)-1) + ")"
	res, err := l.cfg.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("auditlog: insert %q: %w", e.Action, err)
	}
	id, idErr := res.LastInsertId()
	if idErr != nil {
		logger.Warn("auditlog: id of %q for user %d unreadable: %v", e.Action, e.UserID, idErr)
		id = 0
	}
	return id, nil
}

// Record logs an action taken in a request. kv are alternating detail keys and
// values; the keys "entity" (record id), "status" and "scope" are lifted out of
// the details. Failures are logged here and never returned, because an audit
// write must not undo the action that already happened.
func (l *Log) Record(r *http.Request, userID int64, action string, kv ...string) {
	e := Entry{UserID: userID, Action: action, Details: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		k, v := kv[i], kv[i+1]
		switch k {
		case "entity":
			e.EntityID = v
		case "status":
			e.Status = v
		case "scope":
			e.ScopeID, _ = strconv.ParseInt(v, 10, 64)
		default:
			e.Details[k] = v
		}
	}
	if r != nil {
		e.Location = geo.RequestLocation(r)
		e.UserAgent = r.UserAgent()
	}
	if !l.cfg.Silent {
		logger.Info("audit %s user=%d entity=%s", action, userID, e.EntityID)
	}
	if err := l.Write(context.Background(), e); err != nil {
		logger.Error("audit entry %q for user %d could not be stored: %v", action, userID, err)
	}
}

// Filter narrows a listing.
type Filter struct {
	From, To     time.Time
	UserID       int64
	Action       string
	ResourceType string
	Status       string
	ScopeID      int64
	Q            string
}

func (l *Log) timeArg(t time.Time) any {
	if l.cfg.TimeUnix {
		return t.Unix()
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func (l *Log) where(f Filter) (string, []any) {
	w := "1=1"
	var args []any
	a := func(col string) string { return "a." + col }
	if has(l.c.CreatedAt) {
		if !f.From.IsZero() {
			w += " AND " + a(l.c.CreatedAt) + ">=?"
			args = append(args, l.timeArg(f.From))
		}
		if !f.To.IsZero() {
			w += " AND " + a(l.c.CreatedAt) + "<?"
			args = append(args, l.timeArg(f.To))
		}
	}
	if f.UserID > 0 && has(l.c.UserID) {
		w += " AND " + a(l.c.UserID) + "=?"
		args = append(args, f.UserID)
	}
	if f.Action != "" {
		w += " AND " + a(l.c.Action) + "=?"
		args = append(args, f.Action)
	}
	if f.ResourceType != "" && has(l.c.ResourceType) {
		w += " AND " + a(l.c.ResourceType) + "=?"
		args = append(args, f.ResourceType)
	}
	if f.Status != "" && has(l.c.Status) {
		w += " AND " + a(l.c.Status) + "=?"
		args = append(args, f.Status)
	}
	if f.ScopeID > 0 && has(l.c.ScopeID) {
		w += " AND " + a(l.c.ScopeID) + "=?"
		args = append(args, f.ScopeID)
	}
	if f.Q != "" {
		like := "%" + escapeLike(f.Q) + "%"
		var parts []string
		for _, col := range []string{l.c.Action, l.c.EntityID, l.c.Details, l.c.Location} {
			if has(col) {
				parts = append(parts, a(col)+" LIKE ? ESCAPE '\\'")
				args = append(args, like)
			}
		}
		if l.cfg.UsersTable != "" {
			parts = append(parts, "("+l.cfg.UsersLabelExpr+") LIKE ? ESCAPE '\\'")
			args = append(args, like)
		}
		w += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	return w, args
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

func (l *Log) from() string {
	s := l.cfg.Table + " a"
	if l.cfg.UsersTable != "" && has(l.c.UserID) {
		s += " LEFT JOIN " + l.cfg.UsersTable + " u ON u." + l.cfg.UsersIDCol + "=a." + l.c.UserID
	}
	return s
}

func sel(col, alias, fallback string) string {
	if has(col) {
		return "COALESCE(" + alias + "." + col + ", " + fallback + ")"
	}
	return fallback
}

// List returns one page of entries newest first, and the total match count.
func (l *Log) List(ctx context.Context, f Filter, limit, offset int) ([]Entry, int, error) {
	where, args := l.where(f)
	var total int
	if err := l.cfg.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+l.from()+" WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("auditlog: count: %w", err)
	}
	label := "''"
	if l.cfg.UsersTable != "" && has(l.c.UserID) {
		label = "COALESCE(" + l.cfg.UsersLabelExpr + ", '')"
	}
	scope := "0"
	if has(l.c.ScopeID) {
		scope = "COALESCE(a." + l.c.ScopeID + ", 0)"
	}
	created := "NULL"
	if has(l.c.CreatedAt) {
		created = "a." + l.c.CreatedAt
	}
	order := "a." + l.c.ID
	if !has(l.c.ID) {
		order = created
	}
	q := "SELECT " + order + ", " + sel(l.c.UserID, "a", "0") + ", " + label + ", a." + l.c.Action + ", " +
		sel(l.c.ResourceType, "a", "''") + ", " + sel(l.c.EntityID, "a", "''") + ", " + sel(l.c.Status, "a", "''") + ", " + scope + ", " +
		sel(l.c.Details, "a", "''") + ", " + sel(l.c.Location, "a", "''") + ", " + sel(l.c.UserAgent, "a", "''") + ", " + created +
		" FROM " + l.from() + " WHERE " + where + " ORDER BY " + order + " DESC LIMIT ? OFFSET ?"
	rows, err := l.cfg.DB.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("auditlog: list: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var id, at any
		if err := rows.Scan(&id, &e.UserID, &e.UserLabel, &e.Action, &e.ResourceType, &e.EntityID, &e.Status, &e.ScopeID, &e.RawDetails, &e.Location, &e.UserAgent, &at); err != nil {
			return nil, 0, fmt.Errorf("auditlog: scan: %w", err)
		}
		if n, ok := toInt(id); ok {
			e.ID = n
		}
		e.At = toTime(at)
		if e.ResourceType == "" {
			e.ResourceType = ResourceOf(e.Action)
		}
		e.Details = parseDetails(e.RawDetails)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func parseDetails(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]string{"message": raw}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(t)
		default:
			b, _ := json.Marshal(t)
			out[k] = string(b)
		}
	}
	return out
}

func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case []byte:
		n, err := strconv.ParseInt(string(t), 10, 64)
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	}
	return 0, false
}

var timeLayouts = []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02"}

func toTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case int64:
		return time.Unix(t, 0).UTC()
	case []byte:
		return toTime(string(t))
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return time.Unix(n, 0).UTC()
		}
		for _, layout := range timeLayouts {
			if tm, err := time.ParseInLocation(layout, t, time.UTC); err == nil {
				return tm.UTC()
			}
		}
	}
	return time.Time{}
}

// Option is a value and label pair for a filter dropdown.
type Option struct {
	Value string
	Label string
}

// Users lists the users that appear in the trail.
func (l *Log) Users(ctx context.Context) ([]Option, error) {
	if !has(l.c.UserID) {
		return nil, nil
	}
	label := "''"
	if l.cfg.UsersTable != "" {
		label = "COALESCE(" + l.cfg.UsersLabelExpr + ", '')"
	}
	q := "SELECT DISTINCT a." + l.c.UserID + ", " + label + " FROM " + l.from() + " WHERE a." + l.c.UserID + ">0 ORDER BY 2"
	rows, err := l.cfg.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("auditlog: users: %w", err)
	}
	defer rows.Close()
	var out []Option
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("auditlog: scan user: %w", err)
		}
		if name == "" {
			name = "#" + strconv.FormatInt(id, 10)
		}
		out = append(out, Option{Value: strconv.FormatInt(id, 10), Label: name})
	}
	return out, rows.Err()
}

// Actions lists the distinct actions in the trail.
func (l *Log) Actions(ctx context.Context) ([]string, error) {
	rows, err := l.cfg.DB.QueryContext(ctx, "SELECT DISTINCT "+l.c.Action+" FROM "+l.cfg.Table+" ORDER BY 1")
	if err != nil {
		return nil, fmt.Errorf("auditlog: actions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("auditlog: scan action: %w", err)
		}
		out = append(out, a)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// Schema returns the standard table definition for a new product. dialect is
// "sqlite" or "mysql". Existing products keep their table and describe it with
// Config instead.
func Schema(dialect string) string {
	if dialect == "mysql" {
		return `CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL DEFAULT 0,
  action VARCHAR(191) NOT NULL,
  resource_type VARCHAR(191) NOT NULL DEFAULT '',
  entity_id VARCHAR(191) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'ok',
  details TEXT,
  location VARCHAR(191) NOT NULL DEFAULT '',
  user_agent VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_audit_created (created_at),
  KEY idx_audit_action (action),
  KEY idx_audit_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`
	}
	return `CREATE TABLE IF NOT EXISTS audit_logs(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL DEFAULT 0,
	action TEXT NOT NULL,
	resource_type TEXT NOT NULL DEFAULT '',
	entity_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'ok',
	details TEXT NOT NULL DEFAULT '{}',
	location TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id);`
}
