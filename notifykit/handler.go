package notifykit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Config struct {
	Store    Store
	Prefix   string
	UserID   func(r *http.Request) (int64, bool)
	Fallback string

	ResolveLink func(link string) (string, bool)
}

type Handler struct {
	cfg Config
}

func New(cfg Config) *Handler {
	cfg.Prefix = strings.TrimRight(cfg.Prefix, "/")
	if cfg.Fallback == "" {
		cfg.Fallback = "/"
	}
	return &Handler{cfg: cfg}
}

type Summary struct {
	Unread     int             `json:"unread"`
	Grouped    bool            `json:"grouped"`
	Categories []CategoryCount `json:"categories"`
	Items      []Item          `json:"items"`
}

type ListResult struct {
	Items    []Item `json:"items"`
	Page     int    `json:"page"`
	Pages    int    `json:"pages"`
	Total    int    `json:"total"`
	Unread   int    `json:"unread"`
	Category string `json:"category"`
}

func (h *Handler) decorate(items []Item) []Item {
	for i := range items {
		items[i].Category = NormalizeCategory(items[i].Category)
		items[i].HasLink = strings.TrimSpace(items[i].Link) != ""
		items[i].OpenURL = h.cfg.Prefix + "/open/" + strconv.FormatInt(items[i].ID, 10)
	}
	return items
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.cfg.UserID(r)
	if !ok || uid <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, h.cfg.Prefix)
	path = strings.Trim(path, "/")
	switch {
	case path == "summary" && r.Method == http.MethodGet:
		h.summary(w, uid)
	case path == "list" && r.Method == http.MethodGet:
		h.list(w, r, uid)
	case path == "read-category" && r.Method == http.MethodPost:
		h.readCategory(w, r, uid)
	case strings.HasPrefix(path, "read/") && r.Method == http.MethodPost:
		h.read(w, uid, strings.TrimPrefix(path, "read/"))
	case strings.HasPrefix(path, "open/") && r.Method == http.MethodGet:
		h.open(w, r, uid, strings.TrimPrefix(path, "open/"))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) summary(w http.ResponseWriter, uid int64) {
	counts, err := h.cfg.Store.Counts(uid)
	if err != nil {
		logger.Error("notifykit: counts failed user=%d: %v", uid, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
		return
	}
	counts = SortCounts(counts)
	sum := Summary{Unread: TotalUnread(counts), Categories: counts, Items: []Item{}}
	sum.Grouped = sum.Unread > GroupThreshold
	if !sum.Grouped && sum.Unread > 0 {
		items, _, err := h.cfg.Store.List(uid, Filter{UnreadOnly: true, Limit: GroupThreshold})
		if err != nil {
			logger.Error("notifykit: unread list failed user=%d: %v", uid, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
			return
		}
		sum.Items = h.decorate(items)
	}
	writeJSON(w, http.StatusOK, sum)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, uid int64) {
	cat := strings.TrimSpace(r.URL.Query().Get("category"))
	if cat != "" {
		cat = NormalizeCategory(cat)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	items, total, err := h.cfg.Store.List(uid, Filter{Category: cat, Limit: PageSize, Offset: (page - 1) * PageSize})
	if err != nil {
		logger.Error("notifykit: list failed user=%d category=%q page=%d: %v", uid, cat, page, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load_failed"})
		return
	}
	unread := 0
	if counts, cerr := h.cfg.Store.Counts(uid); cerr != nil {
		logger.Error("notifykit: counts failed user=%d: %v", uid, cerr)
	} else {
		for _, c := range counts {
			if cat == "" || NormalizeCategory(c.Category) == cat {
				unread += c.Unread
			}
		}
	}
	pages := (total + PageSize - 1) / PageSize
	if pages < 1 {
		pages = 1
	}
	writeJSON(w, http.StatusOK, ListResult{Items: h.decorate(items), Page: page, Pages: pages, Total: total, Unread: unread, Category: cat})
}

func (h *Handler) readCategory(w http.ResponseWriter, r *http.Request, uid int64) {
	cat := NormalizeCategory(r.FormValue("category"))
	n, err := h.cfg.Store.MarkCategoryRead(uid, cat)
	if err != nil {
		logger.Error("notifykit: mark category read failed user=%d category=%s: %v", uid, cat, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "marked": n})
}

func (h *Handler) read(w http.ResponseWriter, uid int64, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_id"})
		return
	}
	if err := h.cfg.Store.MarkRead(uid, id); err != nil {
		logger.Error("notifykit: mark read failed user=%d id=%d: %v", uid, id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request, uid int64, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		http.Redirect(w, r, h.cfg.Fallback, http.StatusSeeOther)
		return
	}
	it, err := h.cfg.Store.Get(uid, id)
	if err != nil {
		logger.Error("notifykit: open lookup failed user=%d id=%d: %v", uid, id, err)
		http.Redirect(w, r, h.cfg.Fallback, http.StatusSeeOther)
		return
	}
	if !it.Read {
		if err := h.cfg.Store.MarkRead(uid, id); err != nil {
			logger.Error("notifykit: mark read on open failed user=%d id=%d: %v", uid, id, err)
		}
	}
	resolve := h.cfg.ResolveLink
	if resolve == nil {
		resolve = SafeLocalPath
	}
	target, ok := resolve(it.Link)
	if !ok {
		target = h.cfg.Fallback
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Error("notifykit: encode response failed: %v", err)
	}
}
