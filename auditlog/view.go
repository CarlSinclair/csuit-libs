package auditlog

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Line is one labelled value in the details modal, optionally a link.
type Line struct {
	Label string
	Value string
	Link  string
}

// Row is one rendered audit entry.
type Row struct {
	ID       int64
	ISO      string
	Fallback string
	User     string
	UserLink string
	Action   string
	Status   string
	Link     string
	Location string
	Agent    string
	Lines    []Line
}

type View struct {
	Rows       []Row
	Total      int
	Page       int
	Filter     Filter
	DateFrom   string
	DateTo     string
	FilterUser string
	Users      []Option
	Actions    []Option
	Statuses   []Option
	Applied    bool
	Prev       string
	Next       string
	CSV        string
	Base       string
	Err        bool
}

// PageOptions is how a product plugs its own wording, routes and record labels
// into the shared page.
type PageOptions struct {
	// Base is the page path, used for the filter form and paging links.
	Base string
	// PerPage defaults to 50.
	PerPage int
	// T returns the translation of a key, or the key or "" when unknown.
	T func(key string) string
	// Location sets the day boundaries of the date filters (default UTC).
	Location *time.Location
	// UserLabel names a user whose row has no name (for example deleted).
	UserLabel func(id int64) string
	// UserLink is the admin page of a user, when there is one.
	UserLink func(id int64) string
	// EntityLink is the page of the record an entry touched, when there is one.
	EntityLink func(e Entry) string
	// Line lets a product label or link one detail value; return false to use
	// the default handling.
	Line func(e Entry, key, value string) (Line, bool)
	// Icon returns the sprite href of an icon name ("search").
	Icon func(name string) string
	// ShowStatus adds the status filter and column (products that log warn and
	// error entries).
	ShowStatus bool
}

func (o PageOptions) tr(key, fallback string) string {
	if o.T == nil {
		return fallback
	}
	v := o.T(key)
	if v == "" || v == key {
		return fallback
	}
	return v
}

func (o PageOptions) perPage() int {
	if o.PerPage > 0 {
		return o.PerPage
	}
	return 50
}

func (o PageOptions) loc() *time.Location {
	if o.Location != nil {
		return o.Location
	}
	return time.UTC
}

// HumanizeKey turns "course_id" into "Course Id".
func HumanizeKey(k string) string {
	parts := strings.FieldsFunc(k, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for i, p := range parts {
		r := []rune(p)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			parts[i] = string(r)
		}
	}
	return strings.Join(parts, " ")
}

// ActionLabel is the translated name of an action: the product's own
// "audit.action.X", then the shared "auditlog.action.X", then a readable form.
func (o PageOptions) ActionLabel(action string) string {
	if v := o.tr("audit.action."+action, ""); v != "" {
		return v
	}
	if v := o.tr("auditlog.action."+action, ""); v != "" {
		return v
	}
	return HumanizeKey(action)
}

func parseDay(v string, loc *time.Location, nextDay bool) time.Time {
	t, err := time.ParseInLocation("2006-01-02", v, loc)
	if err != nil {
		return time.Time{}
	}
	if nextDay {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

func (o PageOptions) filterFrom(r *http.Request) (Filter, string, string) {
	q := r.URL.Query()
	f := Filter{
		Action:       strings.TrimSpace(q.Get("action")),
		ResourceType: strings.TrimSpace(q.Get("resource")),
		Status:       strings.TrimSpace(q.Get("status")),
		Q:            strings.TrimSpace(q.Get("q")),
	}
	if len(f.Q) > 100 {
		f.Q = f.Q[:100]
	}
	from, to := strings.TrimSpace(q.Get("date_from")), strings.TrimSpace(q.Get("date_to"))
	f.From, f.To = parseDay(from, o.loc(), false), parseDay(to, o.loc(), true)
	f.UserID, _ = strconv.ParseInt(q.Get("user_id"), 10, 64)
	f.ScopeID, _ = strconv.ParseInt(q.Get("scope"), 10, 64)
	return f, from, to
}

func filterQuery(r *http.Request) string {
	q := r.URL.Query()
	var parts []string
	for _, k := range []string{"date_from", "date_to", "user_id", "action", "resource", "status", "scope", "q"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			parts = append(parts, k+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// Page reads the filters of a request and builds everything the page shows.
// Failures are logged and flagged on the view (Err) so the product can show
// its error banner while the page still renders what it could load.
func (l *Log) Page(r *http.Request, o PageOptions) View {
	ctx := r.Context()
	f, from, to := o.filterFrom(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	per := o.perPage()
	v := View{Page: page, Filter: f, DateFrom: from, DateTo: to, FilterUser: strconv.FormatInt(f.UserID, 10), Base: o.Base}
	v.Applied = !f.From.IsZero() || !f.To.IsZero() || f.UserID > 0 || f.Action != "" || f.Status != "" || f.ScopeID > 0 || f.Q != ""
	fail := func(err error) {
		logger.Error("auditlog page %s: %v", o.Base, err)
		v.Err = true
	}
	entries, total, err := l.List(ctx, f, per, (page-1)*per)
	if err != nil {
		fail(err)
	}
	v.Total = total
	v.Rows = make([]Row, 0, len(entries))
	for _, e := range entries {
		v.Rows = append(v.Rows, o.row(e))
	}
	if users, err := l.Users(ctx); err != nil {
		fail(err)
	} else {
		v.Users = users
	}
	if acts, err := l.Actions(ctx); err != nil {
		fail(err)
	} else {
		for _, a := range acts {
			v.Actions = append(v.Actions, Option{Value: a, Label: o.ActionLabel(a)})
		}
		sort.Slice(v.Actions, func(i, j int) bool { return v.Actions[i].Label < v.Actions[j].Label })
	}
	if o.ShowStatus {
		v.Statuses = []Option{{StatusOK, o.tr("auditlog.status_ok", "OK")}, {StatusWarn, o.tr("auditlog.status_warn", "Warning")}, {StatusError, o.tr("auditlog.status_error", "Error")}}
	}
	base := o.Base + "?"
	fq := filterQuery(r)
	if fq != "" {
		base += fq + "&"
	}
	if page > 1 {
		v.Prev = base + "page=" + strconv.Itoa(page-1)
	}
	if page*per < total {
		v.Next = base + "page=" + strconv.Itoa(page+1)
	}
	v.CSV = o.Base + "?" + fq + map[bool]string{true: "&", false: ""}[fq != ""] + "format=csv"
	return v
}

func (o PageOptions) row(e Entry) Row {
	user := e.UserLabel
	if user == "" {
		switch {
		case e.UserID <= 0:
			user = o.tr("auditlog.system", "System")
		case o.UserLabel != nil:
			user = o.UserLabel(e.UserID)
		}
		if user == "" {
			user = o.tr("auditlog.deleted_user", "Deleted user")
		}
	}
	row := Row{ID: e.ID, User: user, Action: o.ActionLabel(e.Action), Status: e.Status, Location: e.Location, Agent: e.UserAgent, Lines: o.lines(e)}
	if !e.At.IsZero() {
		row.ISO = e.At.Format(time.RFC3339)
		row.Fallback = e.At.In(o.loc()).Format("2006-01-02 15:04")
	}
	if o.UserLink != nil && e.UserID > 0 {
		row.UserLink = o.UserLink(e.UserID)
	}
	if o.EntityLink != nil {
		row.Link = o.EntityLink(e)
	}
	return row
}

func (o PageOptions) lines(e Entry) []Line {
	keys := make([]string, 0, len(e.Details))
	for k := range e.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Line, 0, len(keys))
	for _, k := range keys {
		v := e.Details[k]
		if o.Line != nil {
			if l, ok := o.Line(e, k, v); ok {
				out = append(out, l)
				continue
			}
		}
		line := Line{Label: o.tr("audit.key."+k, ""), Value: v}
		if line.Label == "" {
			line.Label = o.tr("auditlog.key."+k, HumanizeKey(k))
		}
		switch v {
		case "true":
			line.Value = o.tr("auditlog.yes", "Yes")
		case "false":
			line.Value = o.tr("auditlog.no", "No")
		}
		out = append(out, line)
	}
	return out
}

// ServeCSV writes the filtered trail (up to 10000 rows) as a CSV download.
func (l *Log) ServeCSV(w http.ResponseWriter, r *http.Request, o PageOptions) {
	f, _, _ := o.filterFrom(r)
	entries, _, err := l.List(r.Context(), f, 10000, 0)
	if err != nil {
		logger.Error("auditlog csv export: %v", err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	w.Header().Set("Cache-Control", "no-store, private")
	cw := csv.NewWriter(w)
	rowErr := cw.Write([]string{"time", "user", "action", "resource", "entity", "status", "location", "details"})
	for _, e := range entries {
		row := o.row(e)
		raw, mErr := json.Marshal(e.Details)
		if mErr != nil {
			logger.Error("auditlog csv export: details of entry %d: %v", e.ID, mErr)
		}
		if err := cw.Write([]string{row.ISO, row.User, row.Action, e.ResourceType, e.EntityID, e.Status, e.Location, string(raw)}); err != nil && rowErr == nil {
			rowErr = err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		rowErr = err
	}
	if rowErr != nil {
		logger.Error("auditlog csv export: write interrupted: %v", rowErr)
	}
}
