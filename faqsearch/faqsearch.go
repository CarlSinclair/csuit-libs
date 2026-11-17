// Package faqsearch ranks a list of FAQ entries against what a person typed.
// The entries live in the caller's own store and that store stays the
// authority on what may be shown: the caller passes the entries someone is
// allowed to see, and the Redis index (when available) only supplies the
// order. A stale index entry can therefore never surface a deleted, disabled
// or private question. Without Redis, or when it fails, the same entries are
// scanned directly with the same folding rules.
package faqsearch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/searchkit"
	"github.com/CarlSinclair/csuit-libs/searchkit/redisindex"
	goredis "github.com/redis/go-redis/v9"
)

const opTimeout = 3 * time.Second

// ErrIndex is returned, wrapped, when a change was stored but the search
// index could not be updated. The nightly reconcile repairs it.
var ErrIndex = errors.New("faq search index not updated")

type Entry struct {
	ID       int64
	Lang     string
	Question string
	Answer   string
}

func (e Entry) version() string {
	h := sha1.New()
	for _, p := range []string{e.Lang, e.Question, e.Answer} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (e Entry) doc() redisindex.Doc {
	return redisindex.Doc{
		Text:    map[string]string{"question": e.Question, "answer": e.Answer},
		Tags:    map[string]string{"lang": e.Lang},
		Version: e.version(),
	}
}

type Options struct {
	Any           bool
	Limit         int
	MinScoreRatio float64
}

type Result struct {
	Entries  []Entry
	Fallback bool
}

// Index is one FAQ index. Name and Prefix carry the caller's own scope (a
// platform, or a tenant and audience) so unrelated lists never share keys.
type Index struct {
	Name   string
	Prefix string
	Client func() *goredis.Client
}

func (x Index) ri() *redisindex.Index {
	return &redisindex.Index{
		Name:   x.Name,
		Prefix: x.Prefix,
		Fields: []redisindex.Field{
			{Name: "question", Kind: redisindex.Text, Weight: 3},
			{Name: "answer", Kind: redisindex.Text, Weight: 1},
			{Name: "lang", Kind: redisindex.Tag},
		},
		Client: x.Client,
	}
}

func (x Index) available() bool { return x.Client != nil && x.Client() != nil }

// Sync stores the entry's current text in the index.
func (x Index) Sync(e Entry) error {
	if !x.available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	ix := x.ri()
	if err := ix.Ensure(ctx); err != nil {
		logger.Error("faq index %s: ensure failed id=%d err=%v", x.Name, e.ID, err)
		return fmt.Errorf("%w: %v", ErrIndex, err)
	}
	if err := ix.Upsert(ctx, strconv.FormatInt(e.ID, 10), e.doc()); err != nil {
		logger.Error("faq index %s: upsert failed id=%d lang=%q err=%v", x.Name, e.ID, e.Lang, err)
		return fmt.Errorf("%w: %v", ErrIndex, err)
	}
	return nil
}

// Remove drops the entry from the index.
func (x Index) Remove(id int64) error {
	if !x.available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	if err := x.ri().Remove(ctx, strconv.FormatInt(id, 10)); err != nil {
		logger.Error("faq index %s: remove failed id=%d err=%v", x.Name, id, err)
		return fmt.Errorf("%w: %v", ErrIndex, err)
	}
	return nil
}

// Reconcile makes the index hold exactly the given entries (the ones that may
// be searched), touching only entries that are missing, changed or gone.
func (x Index) Reconcile(entries []Entry) error {
	if !x.available() {
		return nil
	}
	byID := make(map[string]Entry, len(entries))
	want := make(map[string]string, len(entries))
	for _, e := range entries {
		id := strconv.FormatInt(e.ID, 10)
		byID[id] = e
		want[id] = e.version()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ix := x.ri()
	if err := ix.Ensure(ctx); err != nil {
		logger.Error("faq index %s: reconcile ensure failed err=%v", x.Name, err)
		return fmt.Errorf("faq index %s: ensure: %w", x.Name, err)
	}
	rep, err := ix.Reconcile(ctx, want, func(ids []string) (map[string]redisindex.Doc, error) {
		docs := make(map[string]redisindex.Doc, len(ids))
		for _, id := range ids {
			docs[id] = byID[id].doc()
		}
		return docs, nil
	})
	if err != nil {
		logger.Error("faq index %s: reconcile added=%d updated=%d removed=%d failed=%d err=%v", x.Name, rep.Added, rep.Updated, rep.Removed, rep.Failed, err)
		return fmt.Errorf("faq index %s: reconcile: %w", x.Name, err)
	}
	if rep.Added+rep.Updated+rep.Removed > 0 {
		logger.Warn("faq index %s: reconcile repaired added=%d updated=%d removed=%d", x.Name, rep.Added, rep.Updated, rep.Removed)
	}
	return nil
}

// Search ranks view (every entry the person may see, already in the language
// they are shown) against query. With opts.Any the words are alternatives and
// common words are ignored, which suits a sentence typed into a chat;
// otherwise every word must match.
func (x Index) Search(view []Entry, query string, opts Options) Result {
	words := searchkit.Words(query)
	if len(view) == 0 || len(words) == 0 {
		return Result{}
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	lang := view[0].Lang
	if opts.Any {
		words = searchkit.RemoveStopwords(words, lang)
	}
	byID := make(map[int64]Entry, len(view))
	for _, e := range view {
		byID[e.ID] = e
	}
	if !x.available() {
		logger.Warn("faq search %s: redis not available, scanning entries lang=%q", x.Name, lang)
		return Result{Entries: scan(view, words, opts.Any, limit), Fallback: true}
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	res, err := x.ri().Search(ctx, redisindex.Query{
		Words:         words,
		Fields:        []string{"question", "answer"},
		Any:           opts.Any,
		Tags:          map[string][]string{"lang": {lang}},
		Limit:         limit,
		MinScoreRatio: opts.MinScoreRatio,
	})
	if err != nil {
		logger.Error("faq search %s: index query failed, scanning entries lang=%q words=%d err=%v", x.Name, lang, len(words), err)
		return Result{Entries: scan(view, words, opts.Any, limit), Fallback: true}
	}
	var ranked []Entry
	for _, h := range res.Hits {
		id, convErr := strconv.ParseInt(h.ID, 10, 64)
		if convErr != nil {
			logger.Warn("faq search %s: non-numeric id %q in index: %v", x.Name, h.ID, convErr)
			continue
		}
		if e, ok := byID[id]; ok {
			ranked = append(ranked, e)
		}
	}
	if len(ranked) == 0 {
		if scanned := scan(view, words, opts.Any, limit); len(scanned) > 0 {
			logger.Warn("faq search %s: index returned nothing but the scan found %d, the index may be stale lang=%q", x.Name, len(scanned), lang)
			return Result{Entries: scanned, Fallback: true}
		}
	}
	return Result{Entries: ranked}
}

func scan(view []Entry, words []string, anyWord bool, limit int) []Entry {
	type scored struct {
		e     Entry
		score int
	}
	var hits []scored
	for _, e := range view {
		q := searchkit.IndexWords(e.Question)
		a := searchkit.IndexWords(e.Answer)
		score, matched := 0, 0
		for _, w := range words {
			inQ, inA := hasPrefixWord(q, w), hasPrefixWord(a, w)
			if inQ || inA {
				matched++
			}
			if inQ {
				score += 3
			}
			if inA {
				score++
			}
		}
		if matched == 0 || (!anyWord && matched < len(words)) {
			continue
		}
		hits = append(hits, scored{e: e, score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Entry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.e)
	}
	return out
}

func hasPrefixWord(words []string, prefix string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}
