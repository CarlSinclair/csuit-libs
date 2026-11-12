package helpdesk

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	StatusOpen           = "open"
	StatusAwaitingUser   = "awaiting_user"
	StatusActionRequired = "action_required"
	StatusClosed         = "closed"
	StatusPending        = "pending"
	StatusResolved       = "resolved"

	SourceCustomer = "customer"
	SourceAgent    = "agent"
	SourceTelegram = "telegram"
	SourceStaff    = "staff"

	AuthorUser     = "user"
	AuthorParty    = "party"
	KindCancel     = "cancel"
	KindReturn     = "return"
	ActionHandled  = "handled"
	ActionDeclined = "declined"

	maxBody = 8000
)

var (
	ErrNotFound       = errors.New("helpdesk: ticket not found")
	ErrCancelNotFound = errors.New("helpdesk: cancel request not found or already resolved")
	ErrInvalid        = errors.New("helpdesk: invalid input")
)

type NotifyError struct{ Err error }

func (e *NotifyError) Error() string {
	return "helpdesk: resolved but party not notified: " + e.Err.Error()
}
func (e *NotifyError) Unwrap() error { return e.Err }

type Hooks struct {
	NotifyStaff func(ticketID int64, ref string)
	NotifyParty func(ticketID int64, partyID int64, message, event string) error
	Translate   func(partyID int64, key string) string
	Activity    func(actorID int64, action, target string, meta map[string]any)
}

type Config struct {
	DB          *sql.DB
	PartyColumn string
	RefColumn   string
	Hooks       Hooks
}

type Service struct {
	db    *sql.DB
	party string
	ref   string
	hooks Hooks
}

type Ticket struct {
	ID        int64
	PartyID   int64
	Ref       string
	Subject   string
	Status    string
	Priority  string
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message
}

type Message struct {
	ID         int64
	TicketID   int64
	AuthorID   int64
	AuthorKind string
	Body       string
	Internal   bool
	CreatedAt  time.Time
}

type CreateParams struct {
	PartyID    int64
	Ref        string
	Subject    string
	Body       string
	Source     string
	Status     string
	AuthorKind string
}

func ident(s, def string) (string, error) {
	if s == "" {
		return def, nil
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "", fmt.Errorf("helpdesk: bad column name %q", s)
		}
	}
	return s, nil
}

func New(c Config) (*Service, error) {
	if c.DB == nil {
		return nil, errors.New("helpdesk: DB is required")
	}
	p, err := ident(c.PartyColumn, "user_id")
	if err != nil {
		return nil, err
	}
	r, err := ident(c.RefColumn, "order_id")
	if err != nil {
		return nil, err
	}
	return &Service{db: c.DB, party: p, ref: r, hooks: c.Hooks}, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Service) tr(partyID int64, key string) string {
	if s.hooks.Translate == nil {
		return key
	}
	return s.hooks.Translate(partyID, key)
}

func (s *Service) Create(ctx context.Context, p CreateParams) (int64, error) {
	subject := clip(p.Subject, 500)
	body := clip(p.Body, maxBody)
	if p.PartyID <= 0 || subject == "" || body == "" {
		return 0, ErrInvalid
	}
	if p.Source == "" {
		p.Source = SourceCustomer
	}
	if p.Status == "" {
		p.Status = StatusOpen
	}
	if p.AuthorKind == "" {
		p.AuthorKind = AuthorUser
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("helpdesk Create: begin failed party=%d ref=%q err=%v", p.PartyID, p.Ref, err)
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now()
	var ref any
	if p.Ref != "" {
		ref = p.Ref
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO tickets ("+s.party+", "+s.ref+", subject, status, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		p.PartyID, ref, subject, p.Status, p.Source, now, now)
	if err != nil {
		logger.Error("helpdesk Create: insert ticket failed party=%d ref=%q err=%v", p.PartyID, p.Ref, err)
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		logger.Error("helpdesk Create: reading ticket id failed party=%d ref=%q err=%v", p.PartyID, p.Ref, err)
		return 0, fmt.Errorf("reading new ticket id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO ticket_messages (ticket_id, user_id, author_kind, message, created_at) VALUES (?, ?, ?, ?, ?)",
		id, p.PartyID, p.AuthorKind, body, now); err != nil {
		logger.Error("helpdesk Create: insert message failed ticket_id=%d party=%d err=%v", id, p.PartyID, err)
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		logger.Error("helpdesk Create: commit failed ticket_id=%d party=%d err=%v", id, p.PartyID, err)
		return 0, err
	}
	logger.Info("helpdesk Create: ticket_id=%d party=%d ref=%q source=%s status=%s", id, p.PartyID, p.Ref, p.Source, p.Status)
	if s.hooks.NotifyStaff != nil {
		s.hooks.NotifyStaff(id, p.Ref)
	}
	return id, nil
}

func (s *Service) Reply(ctx context.Context, ticketID, authorID int64, authorKind, body string, internal bool) error {
	body = clip(body, maxBody)
	if ticketID <= 0 || body == "" {
		return ErrInvalid
	}
	if authorKind == "" {
		authorKind = AuthorUser
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		logger.Error("helpdesk Reply: begin failed ticket_id=%d author=%d err=%v", ticketID, authorID, err)
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT id FROM tickets WHERE id = ?", ticketID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		logger.Error("helpdesk Reply: lookup failed ticket_id=%d err=%v", ticketID, err)
		return err
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, "INSERT INTO ticket_messages (ticket_id, user_id, author_kind, message, is_internal, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		ticketID, authorID, authorKind, body, internal, now); err != nil {
		logger.Error("helpdesk Reply: insert message failed ticket_id=%d author=%d err=%v", ticketID, authorID, err)
		return err
	}
	if internal {
		if _, err := tx.ExecContext(ctx, "UPDATE tickets SET updated_at = ? WHERE id = ?", now, ticketID); err != nil {
			logger.Error("helpdesk Reply: touch ticket failed ticket_id=%d err=%v", ticketID, err)
			return err
		}
	} else {
		status := StatusAwaitingUser
		if authorKind == AuthorParty {
			status = StatusOpen
		}
		if _, err := tx.ExecContext(ctx, "UPDATE tickets SET status = ?, updated_at = ? WHERE id = ? AND status <> ?", status, now, ticketID, StatusActionRequired); err != nil {
			logger.Error("helpdesk Reply: update ticket failed ticket_id=%d err=%v", ticketID, err)
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		logger.Error("helpdesk Reply: commit failed ticket_id=%d author=%d err=%v", ticketID, authorID, err)
		return err
	}
	logger.Info("helpdesk Reply: ticket_id=%d author=%d kind=%s internal=%v", ticketID, authorID, authorKind, internal)
	return nil
}

func (s *Service) SetStatus(ctx context.Context, ticketID int64, status string) error {
	switch status {
	case StatusOpen, StatusAwaitingUser, StatusActionRequired, StatusClosed, StatusPending, StatusResolved:
	default:
		return ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, "UPDATE tickets SET status = ?, updated_at = ? WHERE id = ?", status, time.Now(), ticketID)
	if err != nil {
		logger.Error("helpdesk SetStatus: update failed ticket_id=%d status=%s err=%v", ticketID, status, err)
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) scanTicket(row interface{ Scan(...any) error }) (*Ticket, error) {
	var t Ticket
	var ref sql.NullString
	var created, updated sql.NullTime
	if err := row.Scan(&t.ID, &t.PartyID, &ref, &t.Subject, &t.Status, &t.Priority, &t.Source, &created, &updated); err != nil {
		return nil, err
	}
	t.Ref = ref.String
	t.CreatedAt = created.Time
	t.UpdatedAt = updated.Time
	return &t, nil
}

func (s *Service) selectCols() string {
	return "id, " + s.party + ", " + s.ref + ", subject, status, COALESCE(priority, ''), COALESCE(source, ''), created_at, updated_at"
}

func (s *Service) Get(ctx context.Context, id int64) (*Ticket, error) {
	t, err := s.scanTicket(s.db.QueryRowContext(ctx, "SELECT "+s.selectCols()+" FROM tickets WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		logger.Error("helpdesk Get: ticket_id=%d err=%v", id, err)
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id, ticket_id, user_id, COALESCE(author_kind, 'user'), message, is_internal, created_at FROM ticket_messages WHERE ticket_id = ? ORDER BY id ASC", id)
	if err != nil {
		logger.Error("helpdesk Get: loading messages ticket_id=%d err=%v", id, err)
		return nil, fmt.Errorf("loading messages for ticket %d: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Message
		var created sql.NullTime
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.AuthorKind, &m.Body, &m.Internal, &created); err != nil {
			logger.Error("helpdesk Get: scanning message ticket_id=%d err=%v", id, err)
			return nil, err
		}
		m.CreatedAt = created.Time
		t.Messages = append(t.Messages, m)
	}
	if err := rows.Err(); err != nil {
		logger.Error("helpdesk Get: iterating messages ticket_id=%d err=%v", id, err)
		return nil, err
	}
	return t, nil
}

func (s *Service) List(ctx context.Context, status string, limit, offset int) ([]Ticket, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where, args := "", []any{}
	if status != "" {
		where = " WHERE status = ?"
		args = append(args, status)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tickets"+where, args...).Scan(&total); err != nil {
		logger.Error("helpdesk List: count failed status=%q err=%v", status, err)
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.selectCols()+" FROM tickets"+where+" ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		logger.Error("helpdesk List: query failed status=%q err=%v", status, err)
		return nil, 0, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := s.scanTicket(rows)
		if err != nil {
			logger.Error("helpdesk List: scan failed err=%v", err)
			return nil, 0, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		logger.Error("helpdesk List: iterate failed err=%v", err)
		return nil, 0, err
	}
	return out, total, nil
}

func (s *Service) CreateCancelRequest(ctx context.Context, ref string, label string, partyID int64, kind, source, reason, state string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || partyID <= 0 {
		return 0, ErrInvalid
	}
	var existing int64
	err := s.db.QueryRowContext(ctx, "SELECT id FROM tickets WHERE "+s.ref+" = ? AND "+s.party+" = ? AND status = ? ORDER BY id DESC LIMIT 1", ref, partyID, StatusActionRequired).Scan(&existing)
	if err == nil {
		logger.Info("helpdesk CreateCancelRequest: pending request already exists ticket_id=%d ref=%s", existing, ref)
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		logger.Error("helpdesk CreateCancelRequest: duplicate check failed ref=%s err=%v", ref, err)
		return 0, err
	}
	reason = clip(reason, 2000)
	if reason == "" {
		reason = s.tr(partyID, "cancel_request_no_reason")
	}
	subjectKey := "cancel_request_ticket_subject_cancel"
	if kind == KindReturn {
		subjectKey = "cancel_request_ticket_subject_return"
	}
	sourceKey := "cancel_request_source_customer"
	if source == SourceAgent {
		sourceKey = "cancel_request_source_agent"
	}
	subject := fmt.Sprintf(s.tr(partyID, subjectKey), label)
	body := fmt.Sprintf(s.tr(partyID, "cancel_request_ticket_content"), s.tr(partyID, sourceKey), state, reason)
	if source != SourceAgent {
		source = SourceCustomer
	}
	return s.Create(ctx, CreateParams{PartyID: partyID, Ref: ref, Subject: subject, Body: body, Source: source, Status: StatusActionRequired, AuthorKind: AuthorParty})
}

func (s *Service) PendingCancelRequests(ctx context.Context, limit int) ([]Ticket, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+s.selectCols()+" FROM tickets WHERE status = ? AND "+s.ref+" IS NOT NULL ORDER BY created_at ASC LIMIT ?", StatusActionRequired, limit)
	if err != nil {
		logger.Error("helpdesk PendingCancelRequests: query failed err=%v", err)
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		t, err := s.scanTicket(rows)
		if err != nil {
			logger.Error("helpdesk PendingCancelRequests: scan failed err=%v", err)
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		logger.Error("helpdesk PendingCancelRequests: iterate failed err=%v", err)
		return nil, err
	}
	return out, nil
}

func (s *Service) FirstMessage(ctx context.Context, ticketID int64) string {
	var m string
	if err := s.db.QueryRowContext(ctx, "SELECT message FROM ticket_messages WHERE ticket_id = ? ORDER BY id ASC LIMIT 1", ticketID).Scan(&m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		logger.Warn("helpdesk FirstMessage: ticket_id=%d err=%v", ticketID, err)
	}
	return m
}

func (s *Service) CountPending(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tickets WHERE status = ? AND "+s.ref+" IS NOT NULL", StatusActionRequired).Scan(&n); err != nil {
		logger.Error("helpdesk CountPending: query failed err=%v", err)
		return 0, err
	}
	return n, nil
}

func (s *Service) CloseByRefs(ctx context.Context, refs []string) error {
	for _, ref := range refs {
		if _, err := s.db.ExecContext(ctx, "UPDATE tickets SET status = ?, updated_at = ? WHERE status = ? AND "+s.ref+" = ?", StatusClosed, time.Now(), StatusActionRequired, ref); err != nil {
			logger.Error("helpdesk CloseByRefs: update failed ref=%s err=%v", ref, err)
			return err
		}
	}
	return nil
}

func (s *Service) ResolveCancelRequest(ctx context.Context, ticketID, resolverID int64, action, note string) error {
	replyKey := "cancel_request_reply_handled"
	if action == ActionDeclined {
		replyKey = "cancel_request_reply_declined"
	} else if action != ActionHandled {
		return ErrInvalid
	}
	note = clip(note, 500)
	t, err := s.Get(ctx, ticketID)
	if err != nil || t.Status != StatusActionRequired {
		if err != nil && !errors.Is(err, ErrNotFound) {
			logger.Error("helpdesk ResolveCancelRequest: lookup failed ticket_id=%d err=%v", ticketID, err)
		}
		return ErrCancelNotFound
	}
	reply := strings.TrimSpace(fmt.Sprintf(s.tr(t.PartyID, replyKey), note))
	if err := s.Reply(ctx, ticketID, resolverID, AuthorUser, reply, false); err != nil {
		logger.Error("helpdesk ResolveCancelRequest: reply failed ticket_id=%d err=%v", ticketID, err)
		return err
	}
	if err := s.SetStatus(ctx, ticketID, StatusClosed); err != nil {
		logger.Error("helpdesk ResolveCancelRequest: closing failed ticket_id=%d err=%v", ticketID, err)
		return err
	}
	if s.hooks.Activity != nil {
		s.hooks.Activity(resolverID, "cancel_request_"+action, t.Ref, map[string]any{"ticket_id": ticketID, "note": note})
	}
	logger.Info("helpdesk ResolveCancelRequest: ticket_id=%d ref=%s action=%s resolver=%d", ticketID, t.Ref, action, resolverID)
	if s.hooks.NotifyParty != nil {
		if err := s.hooks.NotifyParty(ticketID, t.PartyID, reply, "reply"); err != nil {
			logger.Error("helpdesk ResolveCancelRequest: notifying party failed ticket_id=%d party=%d err=%v", ticketID, t.PartyID, err)
			return &NotifyError{Err: err}
		}
	}
	return nil
}
