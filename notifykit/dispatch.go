package notifykit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Delivery channels. In-app is the bell; push is Web Push to the browser.
const (
	ChanInApp    = "inapp"
	ChanEmail    = "email"
	ChanTelegram = "telegram"
	ChanPush     = "push"
)

// AllChannels is the order channels are tried and listed in.
var AllChannels = []string{ChanInApp, ChanEmail, ChanTelegram, ChanPush}

// EventDef declares one notification a product can send.
type EventDef struct {
	Name     string
	Category string
	// Defaults are the channels on until the person chooses otherwise.
	Defaults []string
	// Locked channels cannot be turned off (security events by email).
	Locked []string
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Recipient is who a notification goes to.
type Recipient struct {
	UserID         int64
	Name           string
	Email          string
	Lang           string
	TelegramChatID int64
}

// Rendered is a notification in the recipient's language.
type Rendered struct {
	Title string
	Body  string
	Link  string
}

// Hooks are the product's side: who the person is, their wording, and the
// actual senders. A nil sender means the product has no such channel.
type Hooks struct {
	Directory func(ctx context.Context, userID int64) (Recipient, error)
	Render    func(ctx context.Context, rcpt Recipient, event string, data map[string]string) (Rendered, error)
	Email     func(ctx context.Context, rcpt Recipient, event string, m Rendered) error
	Telegram  func(ctx context.Context, chatID int64, m Rendered) error
	Push      func(ctx context.Context, userID int64, m Rendered, unread int64) error
	// Admins lists the user ids that receive admin-facing events.
	Admins func(ctx context.Context) ([]int64, error)
}

// Dispatcher sends an event to a person on every channel they allow.
type Dispatcher struct {
	events map[string]EventDef
	store  *SQLStore
	h      Hooks
}

func NewDispatcher(events []EventDef, store *SQLStore, h Hooks) (*Dispatcher, error) {
	if store == nil || h.Directory == nil || h.Render == nil {
		return nil, errors.New("notifykit: store, Directory and Render are required")
	}
	m := make(map[string]EventDef, len(events))
	for _, e := range events {
		if e.Name == "" {
			return nil, errors.New("notifykit: event without a name")
		}
		e.Category = NormalizeCategory(e.Category)
		m[e.Name] = e
	}
	return &Dispatcher{events: m, store: store, h: h}, nil
}

// Events returns the declared events in category order, for the preferences
// panel.
func (d *Dispatcher) Events() []EventDef {
	out := make([]EventDef, 0, len(d.events))
	for _, e := range d.events {
		out = append(out, e)
	}
	sortEvents(out)
	return out
}

// Registry maps event names to categories for the bell.
func (d *Dispatcher) Registry() Registry {
	r := Registry{}
	for n, e := range d.events {
		r[n] = e.Category
	}
	return r
}

func (d *Dispatcher) channelOn(ev EventDef, userID int64, ch string) (bool, error) {
	if contains(ev.Locked, ch) {
		return true, nil
	}
	enabled, set, err := d.store.Pref(userID, ev.Category, ch)
	if err != nil {
		return false, err
	}
	if set {
		return enabled, nil
	}
	return contains(ev.Defaults, ch), nil
}

// Notify sends an event to one person. Each channel is tried independently;
// failures are logged with what was attempted and returned together, so a
// broken channel never hides a working one.
func (d *Dispatcher) Notify(ctx context.Context, userID int64, event string, data map[string]string) error {
	ev, ok := d.events[event]
	if !ok {
		return fmt.Errorf("notifykit: unknown event %q", event)
	}
	rcpt, err := d.h.Directory(ctx, userID)
	if err != nil {
		logger.Error("notifykit: event %s: looking up user %d: %v", event, userID, err)
		return fmt.Errorf("directory lookup for user %d: %w", userID, err)
	}
	rcpt.UserID = userID
	msg, err := d.h.Render(ctx, rcpt, event, data)
	if err != nil {
		logger.Error("notifykit: event %s: rendering for user %d: %v", event, userID, err)
		return fmt.Errorf("render %s for user %d: %w", event, userID, err)
	}
	var errs []error
	record := func(ch string, err error) {
		if err != nil {
			logger.Error("notifykit: event %s to user %d over %s failed: %v", event, userID, ch, err)
			errs = append(errs, fmt.Errorf("%s: %w", ch, err))
		}
	}
	for _, ch := range AllChannels {
		on, err := d.channelOn(ev, userID, ch)
		if err != nil {
			record(ch, fmt.Errorf("reading preference: %w", err))
			continue
		}
		if !on {
			continue
		}
		switch ch {
		case ChanInApp:
			record(ch, d.store.Add(userID, Item{Event: event, Category: ev.Category, Title: msg.Title, Body: msg.Body, Link: msg.Link, Extra: data}))
		case ChanEmail:
			if d.h.Email == nil || rcpt.Email == "" {
				continue
			}
			record(ch, d.h.Email(ctx, rcpt, event, msg))
		case ChanTelegram:
			if d.h.Telegram == nil || rcpt.TelegramChatID == 0 {
				continue
			}
			record(ch, d.h.Telegram(ctx, rcpt.TelegramChatID, msg))
		case ChanPush:
			if d.h.Push == nil {
				continue
			}
			unread, uerr := d.store.Unread(userID)
			if uerr != nil {
				logger.Warn("notifykit: unread count for user %d: %v", userID, uerr)
			}
			record(ch, d.h.Push(ctx, userID, msg, unread))
		}
	}
	return errors.Join(errs...)
}

// NotifyMany sends to several people and returns every failure.
func (d *Dispatcher) NotifyMany(ctx context.Context, userIDs []int64, event string, data map[string]string) error {
	var errs []error
	seen := map[int64]bool{}
	for _, id := range userIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if err := d.Notify(ctx, id, event, data); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// NotifyAdmins sends an event to every admin the product lists, except skip.
func (d *Dispatcher) NotifyAdmins(ctx context.Context, event string, data map[string]string, skip int64) error {
	if d.h.Admins == nil {
		return errors.New("notifykit: Admins hook not set")
	}
	ids, err := d.h.Admins(ctx)
	if err != nil {
		logger.Error("notifykit: event %s: listing admins: %v", event, err)
		return fmt.Errorf("list admins: %w", err)
	}
	var keep []int64
	for _, id := range ids {
		if id != skip {
			keep = append(keep, id)
		}
	}
	return d.NotifyMany(ctx, keep, event, data)
}

// NotifyOnce sends only the first time a (user, event, key) is seen, for
// reminders a job may run more than once. It reports whether it sent.
func (d *Dispatcher) NotifyOnce(ctx context.Context, userID int64, event, key string, data map[string]string) (bool, error) {
	first, err := d.store.Once(userID, event, key)
	if err != nil {
		logger.Error("notifykit: dedupe of %s/%s for user %d: %v", event, key, userID, err)
		return false, err
	}
	if !first {
		return false, nil
	}
	if err := d.Notify(ctx, userID, event, data); err != nil {
		if uerr := d.store.Forget(userID, event, key); uerr != nil {
			logger.Error("notifykit: could not release %s/%s for user %d after a failed send: %v", event, key, userID, uerr)
		}
		return true, err
	}
	return true, nil
}

func sortEvents(list []EventDef) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && less(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func less(a, b EventDef) bool {
	ra, rb := Rank(a.Category), Rank(b.Category)
	if ra != rb {
		return ra < rb
	}
	return strings.Compare(a.Name, b.Name) < 0
}

// DataInt is a small helper for hooks reading numeric template data.
func DataInt(data map[string]string, key string) int64 {
	n, _ := strconv.ParseInt(data[key], 10, 64)
	return n
}
