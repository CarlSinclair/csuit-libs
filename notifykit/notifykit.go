package notifykit

import (
	"sort"
	"strings"
	"time"
)

const (
	CatSecurity   = "security"
	CatBilling    = "billing"
	CatSupport    = "support"
	CatModeration = "moderation"
	CatOrders     = "orders"
	CatBookings   = "bookings"
	CatSocial     = "social"
	CatEpisodes   = "episodes"
	CatSystem     = "system"
	CatOther      = "other"

	GroupThreshold = 5
	PageSize       = 20
	PreviewItems   = 5
)

var categoryOrder = []string{
	CatSecurity, CatBilling, CatSupport, CatModeration,
	CatOrders, CatBookings, CatSocial, CatEpisodes, CatSystem, CatOther,
}

var categoryRank = func() map[string]int {
	m := make(map[string]int, len(categoryOrder))
	for i, c := range categoryOrder {
		m[c] = i
	}
	return m
}()

func Categories() []string {
	out := make([]string, len(categoryOrder))
	copy(out, categoryOrder)
	return out
}

func NormalizeCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	if _, ok := categoryRank[cat]; ok {
		return cat
	}
	return CatOther
}

func Rank(cat string) int {
	return categoryRank[NormalizeCategory(cat)]
}

type Registry map[string]string

func (r Registry) Category(event string) string {
	if cat, ok := r[strings.TrimSpace(event)]; ok {
		return NormalizeCategory(cat)
	}
	return CatOther
}

type Item struct {
	ID        int64             `json:"id"`
	Event     string            `json:"event"`
	Category  string            `json:"category"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Link      string            `json:"-"`
	OpenURL   string            `json:"open_url"`
	HasLink   bool              `json:"has_link"`
	Read      bool              `json:"read"`
	CreatedAt time.Time         `json:"created_at"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type CategoryCount struct {
	Category    string    `json:"category"`
	Unread      int       `json:"unread"`
	Total       int       `json:"total"`
	LatestTitle string    `json:"latest_title"`
	LatestAt    time.Time `json:"latest_at"`
}

type Filter struct {
	Category   string
	UnreadOnly bool
	Limit      int
	Offset     int
}

type Store interface {
	Counts(userID int64) ([]CategoryCount, error)
	List(userID int64, f Filter) ([]Item, int, error)
	Get(userID, id int64) (Item, error)
	MarkRead(userID, id int64) error
	MarkCategoryRead(userID int64, category string) (int64, error)
}

func SortCounts(counts []CategoryCount) []CategoryCount {
	out := make([]CategoryCount, 0, len(counts))
	merged := map[string]*CategoryCount{}
	for _, c := range counts {
		cat := NormalizeCategory(c.Category)
		m, ok := merged[cat]
		if !ok {
			cp := c
			cp.Category = cat
			merged[cat] = &cp
			continue
		}
		m.Unread += c.Unread
		m.Total += c.Total
		if c.LatestAt.After(m.LatestAt) {
			m.LatestAt = c.LatestAt
			m.LatestTitle = c.LatestTitle
		}
	}
	for _, m := range merged {
		out = append(out, *m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, hj := out[i].Unread > 0, out[j].Unread > 0
		if hi != hj {
			return hi
		}
		if Rank(out[i].Category) != Rank(out[j].Category) {
			return Rank(out[i].Category) < Rank(out[j].Category)
		}
		return out[i].LatestAt.After(out[j].LatestAt)
	})
	return out
}

func TotalUnread(counts []CategoryCount) int {
	n := 0
	for _, c := range counts {
		n += c.Unread
	}
	return n
}

func SafeLocalPath(link string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" || link[0] != '/' {
		return "", false
	}
	if len(link) > 1 && (link[1] == '/' || link[1] == '\\') {
		return "", false
	}
	if strings.ContainsAny(link, "\r\n\\") {
		return "", false
	}
	return link, true
}
