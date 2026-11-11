package redisadmin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

const maxKeyScanIters = 60

type Section struct {
	Total int64
	Page  int
	Pages int
	Prev  int
	Next  int
}

func newSection(total int64, page, pageSize int) Section {
	p := NewPager(total, page, pageSize)
	s := Section{Total: total, Page: p.Page, Pages: p.Pages}
	if p.Page > 1 {
		s.Prev = p.Page - 1
	}
	if p.Page < p.Pages {
		s.Next = p.Page + 1
	}
	return s
}

type Overview struct {
	EmailQueueSize      int64
	DeadLetterQueueSize int64
	PageSize            int

	EmailTotal int64
	EmailPage  int
	EmailPages int
	EmailPrev  int
	EmailNext  int
	EmailTasks []redis.EmailTask

	DeadTotal int64
	DeadPage  int
	DeadPages int
	DeadPrev  int
	DeadNext  int
	DeadTasks []redis.EmailTask

	SentTotal   int64
	SentPage    int
	SentPages   int
	SentPrev    int
	SentNext    int
	SentRecords []redis.EmailSentRecord

	EmailErrTotal   int64
	EmailErrPage    int
	EmailErrPages   int
	EmailErrPrev    int
	EmailErrNext    int
	EmailErrRecords []redis.EmailErrorRecord

	KeyPage int
	Errors  []LoadError
}

type LoadError struct {
	Part string
	Err  string
}

func (o *Overview) addErr(part string, err error) {
	o.Errors = append(o.Errors, LoadError{Part: part, Err: err.Error()})
}

func (o Overview) FormatErrors(tmpl string) []string {
	out := make([]string, 0, len(o.Errors))
	for _, e := range o.Errors {
		out = append(out, strings.NewReplacer("{part}", e.Part, "{error}", e.Err).Replace(tmpl))
	}
	return out
}

type Handler struct {
	Path   string
	Render func(w http.ResponseWriter, r *http.Request, ov *Overview)
	Flash  func(w http.ResponseWriter, r *http.Request, kind, key string)
	Send   func(redis.EmailTask) error
	Extra  func(w http.ResponseWriter, r *http.Request, action string) bool
}

var Messages = map[string]string{
	"queue_cleared":   "Queue cleared.",
	"system_error":    "Something went wrong. Check the server log.",
	"empty_or_error":  "The queue is empty or could not be read.",
	"invalid_item":    "That queue item could not be read.",
	"send_failed":     "Sending failed. The task was moved to the dead letter queue.",
	"processed_one":   "One task sent.",
	"requeue_failed":  "Could not requeue the task.",
	"dlq_empty":       "The dead letter queue is empty.",
	"requeued_one":    "One task requeued.",
	"missing_item":    "No item was given.",
	"item_deleted":    "Item deleted.",
	"moved_to_dlq":    "Moved to the dead letter queue.",
	"requeued_item":   "Item requeued.",
	"resend_failed":   "Could not queue the email again.",
	"resend_queued":   "Email queued again.",
	"redis_not_ready": "Redis is not connected.",
}

func (h *Handler) path() string {
	if h.Path == "" {
		return "/redis"
	}
	return h.Path
}

func (h *Handler) flash(w http.ResponseWriter, r *http.Request, kind, key string) {
	if h.Flash != nil {
		h.Flash(w, r, kind, key)
	}
}

func (h *Handler) done(w http.ResponseWriter, r *http.Request, kind, key string) {
	h.flash(w, r, kind, key)
	http.Redirect(w, r, h.path(), http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("redisadmin: response encode failed status=%d err=%v", status, err)
	}
}

func ScanKeysBounded(pattern string, startCursor uint64, want int) (out []KeySummary, next uint64, scanned int, err error) {
	c, cerr := client()
	if cerr != nil {
		return nil, 0, 0, cerr
	}
	if want <= 0 {
		want = 25
	}
	cursor := startCursor
	for i := 0; i < maxKeyScanIters; i++ {
		batch, n, e := c.Scan(redis.Ctx, cursor, pattern, 500).Result()
		if e != nil {
			return out, cursor, scanned, e
		}
		scanned += len(batch)
		for _, k := range batch {
			out = append(out, Summary(k))
			if len(out) >= want {
				return out, n, scanned, nil
			}
		}
		cursor = n
		if cursor == 0 {
			return out, 0, scanned, nil
		}
	}
	return out, cursor, scanned, nil
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	if redis.Client() == nil {
		logger.Error("redisadmin: overview requested while redis is not connected")
		h.done(w, r, "error", "redis_not_ready")
		return
	}
	var ov Overview
	ov.PageSize = DefaultPageSize
	q := r.URL.Query()
	pageOf := func(name string) int { return ClampPage(AtoiDefault(q.Get(name), 1)) }

	var err error
	if ov.EmailQueueSize, err = ListLen(redis.EmailQueueName); err != nil {
		logger.Error("redisadmin: email queue length failed err=%v", err)
		ov.addErr("email queue length", err)
	}
	if ov.DeadLetterQueueSize, err = ListLen(redis.EmailQueueDeadName); err != nil {
		logger.Error("redisadmin: dead letter queue length failed err=%v", err)
		ov.addErr("dead letter queue length", err)
	}
	ov.KeyPage = pageOf("kp")

	s := newSection(ov.EmailQueueSize, pageOf("ep"), ov.PageSize)
	ov.EmailTotal, ov.EmailPage, ov.EmailPages, ov.EmailPrev, ov.EmailNext = s.Total, s.Page, s.Pages, s.Prev, s.Next
	if tasks, err := EmailTasks(redis.EmailQueueName, ov.EmailPage, ov.PageSize); err != nil {
		logger.Error("redisadmin: load email queue page=%d err=%v", ov.EmailPage, err)
		ov.addErr("email queue", err)
		ov.EmailTasks = []redis.EmailTask{}
	} else {
		ov.EmailTasks = tasks
	}

	s = newSection(ov.DeadLetterQueueSize, pageOf("dp"), ov.PageSize)
	ov.DeadTotal, ov.DeadPage, ov.DeadPages, ov.DeadPrev, ov.DeadNext = s.Total, s.Page, s.Pages, s.Prev, s.Next
	if tasks, err := EmailTasks(redis.EmailQueueDeadName, ov.DeadPage, ov.PageSize); err != nil {
		logger.Error("redisadmin: load dead letter page=%d err=%v", ov.DeadPage, err)
		ov.addErr("dead letter queue", err)
		ov.DeadTasks = []redis.EmailTask{}
	} else {
		ov.DeadTasks = tasks
	}

	sentTotal, err := LogLen(redis.EmailSentLogName)
	if err != nil {
		logger.Error("redisadmin: email sent log length failed err=%v", err)
		ov.addErr("sent log length", err)
	}
	s = newSection(sentTotal, pageOf("sp"), ov.PageSize)
	ov.SentTotal, ov.SentPage, ov.SentPages, ov.SentPrev, ov.SentNext = s.Total, s.Page, s.Pages, s.Prev, s.Next
	if recs, err := SentRecords(ov.SentPage, ov.PageSize); err != nil {
		logger.Error("redisadmin: load email sent log page=%d err=%v", ov.SentPage, err)
		ov.addErr("sent log", err)
		ov.SentRecords = []redis.EmailSentRecord{}
	} else {
		ov.SentRecords = recs
	}

	errTotal, err := LogLen(redis.EmailErrorLogName)
	if err != nil {
		logger.Error("redisadmin: email error log length failed err=%v", err)
		ov.addErr("error log length", err)
	}
	s = newSection(errTotal, pageOf("eep"), ov.PageSize)
	ov.EmailErrTotal, ov.EmailErrPage, ov.EmailErrPages, ov.EmailErrPrev, ov.EmailErrNext = s.Total, s.Page, s.Pages, s.Prev, s.Next
	if recs, err := ErrorRecords(ov.EmailErrPage, ov.PageSize); err != nil {
		logger.Error("redisadmin: load email error log page=%d err=%v", ov.EmailErrPage, err)
		ov.addErr("error log", err)
		ov.EmailErrRecords = []redis.EmailErrorRecord{}
	} else {
		ov.EmailErrRecords = recs
	}

	h.Render(w, r, &ov)
}

func (h *Handler) Keys(w http.ResponseWriter, r *http.Request) {
	pattern := "*"
	if redis.CurrentPrefix() != "" {
		pattern = redis.CurrentPrefix() + "*"
	}
	var cursor uint64
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			logger.Warn("redisadmin: keys browse invalid cursor=%q err=%v", raw, err)
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false})
			return
		}
		cursor = parsed
	}
	keys, next, scanned, err := ScanKeysBounded(pattern, cursor, 25)
	if err != nil {
		logger.Error("redisadmin: keys browse scan failed cursor=%d err=%v", cursor, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"keys":        keys,
		"next_cursor": strconv.FormatUint(next, 10),
		"scanned":     scanned,
		"done":        next == 0,
	})
}

func (h *Handler) Action(w http.ResponseWriter, r *http.Request) {
	if redis.Client() == nil {
		logger.Error("redisadmin: action requested while redis is not connected")
		h.done(w, r, "error", "redis_not_ready")
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	queueName := strings.TrimSpace(r.FormValue("queue"))

	if h.Extra != nil && h.Extra(w, r, action) {
		return
	}

	target := redis.EmailQueueName
	if queueName == "dead-letter" {
		target = redis.EmailQueueDeadName
	}

	switch action {
	case "clear":
		var err error
		if queueName == "all" || queueName == "" {
			err = ClearQueues(redis.EmailQueueName, redis.EmailQueueDeadName)
		} else {
			err = ClearQueues(target)
		}
		if err != nil {
			logger.Error("redisadmin: clear queue=%q failed err=%v", queueName, err)
			h.done(w, r, "error", "system_error")
			return
		}
		logger.Info("redisadmin: cleared queue=%q", queueName)
		h.done(w, r, "success", "queue_cleared")
	case "run_one":
		if h.Send == nil {
			logger.Error("redisadmin: run_one requested but no sender is configured")
			h.done(w, r, "error", "system_error")
			return
		}
		ran, err := RunOne(h.Send)
		switch {
		case err != nil && !ran:
			logger.Error("redisadmin: run_one failed to read the queue err=%v", err)
			h.done(w, r, "error", "empty_or_error")
		case err != nil:
			logger.Error("redisadmin: run_one send failed err=%v", err)
			h.done(w, r, "error", "send_failed")
		case !ran:
			h.done(w, r, "error", "empty_or_error")
		default:
			h.done(w, r, "success", "processed_one")
		}
	case "requeue_one":
		ok, err := redis.RequeueDeadLetter()
		if err != nil {
			logger.Error("redisadmin: requeue_one failed err=%v", err)
			h.done(w, r, "error", "requeue_failed")
			return
		}
		if !ok {
			h.done(w, r, "error", "dlq_empty")
			return
		}
		h.done(w, r, "success", "requeued_one")
	case "delete_item", "move_to_dlq", "requeue_item":
		raw := r.FormValue("raw")
		if strings.TrimSpace(raw) == "" {
			h.done(w, r, "error", "missing_item")
			return
		}
		var err error
		okKey := "item_deleted"
		switch action {
		case "delete_item":
			err = DeleteItem(target, raw)
		case "move_to_dlq":
			err = MoveItem(redis.EmailQueueName, redis.EmailQueueDeadName, raw)
			okKey = "moved_to_dlq"
		default:
			err = MoveItem(redis.EmailQueueDeadName, redis.EmailQueueName, raw)
			okKey = "requeued_item"
		}
		if err != nil {
			logger.Error("redisadmin: %s failed queue=%q bytes=%d err=%v", action, queueName, len(raw), err)
			h.done(w, r, "error", "system_error")
			return
		}
		h.done(w, r, "success", okKey)
	case "resend_error_item":
		raw := strings.TrimSpace(r.FormValue("raw_task"))
		if raw == "" {
			logger.Warn("redisadmin: resend_error_item missing raw_task")
			h.done(w, r, "error", "missing_item")
			return
		}
		var task redis.EmailTask
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			logger.Error("redisadmin: resend_error_item unreadable task bytes=%d err=%v", len(raw), err)
			h.done(w, r, "error", "invalid_item")
			return
		}
		if err := ResendTask(raw); err != nil {
			logger.Error("redisadmin: resend_error_item enqueue failed to=%q err=%v", task.ToEmail, err)
			h.done(w, r, "error", "resend_failed")
			return
		}
		logger.Info("redisadmin: resend_error_item queued to=%q template=%q", task.ToEmail, task.TemplateName)
		h.done(w, r, "success", "resend_queued")
	case "inspect":
		items, err := Inspect(target, 50)
		if err != nil {
			logger.Error("redisadmin: inspect failed queue=%q err=%v", target, err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false})
			return
		}
		writeJSON(w, http.StatusOK, items)
	default:
		http.Redirect(w, r, h.path(), http.StatusSeeOther)
	}
}
