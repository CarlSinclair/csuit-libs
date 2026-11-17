// Package redisindex keeps a keyword search index in Redis (the query engine
// built into Redis 8) for documents that live in another store. Text is
// folded with searchkit before it is indexed and before it is queried, so the
// engine never has to know about accents, case or scripts. Documents are
// plain hashes under a key prefix; changing one document touches only that
// hash, and Reconcile repairs drift by comparing versions instead of
// rebuilding everything.
package redisindex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CarlSinclair/csuit-libs/searchkit"
	goredis "github.com/redis/go-redis/v9"
)

const versionField = "_v"

type Kind int

const (
	Text Kind = iota
	Tag
	Numeric
)

type Field struct {
	Name     string
	Kind     Kind
	Weight   float64
	Sortable bool
}

type Index struct {
	Name   string
	Prefix string
	Fields []Field
	Client func() *goredis.Client
}

type Doc struct {
	Text    map[string]string
	Tags    map[string]string
	Nums    map[string]float64
	Version string
}

type Query struct {
	Text          string
	Fields        []string
	Words         []string
	Any           bool
	Tags          map[string][]string
	Ranges        map[string][2]float64
	SortBy        string
	Desc          bool
	Offset        int
	Limit         int
	MinScoreRatio float64
}

type Hit struct {
	ID    string
	Score float64
}

type Result struct {
	Total int
	Hits  []Hit
}

type Report struct {
	Added   int
	Updated int
	Removed int
	Failed  int
}

var ErrNoClient = errors.New("redisindex: redis client not available")

func (ix *Index) client() (*goredis.Client, error) {
	if ix.Client == nil {
		return nil, ErrNoClient
	}
	c := ix.Client()
	if c == nil {
		return nil, ErrNoClient
	}
	return c, nil
}

func (ix *Index) field(name string) (Field, bool) {
	for _, f := range ix.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func isMissingIndex(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unknown index") || strings.Contains(m, "no such index") || strings.Contains(m, "not found")
}

func (ix *Index) Ensure(ctx context.Context) error {
	c, err := ix.client()
	if err != nil {
		return err
	}
	infoErr := c.FTInfo(ctx, ix.Name).Err()
	if infoErr == nil {
		return nil
	}
	if !isMissingIndex(infoErr) {
		return fmt.Errorf("redisindex: info index=%q: %w", ix.Name, infoErr)
	}
	schema := make([]*goredis.FieldSchema, 0, len(ix.Fields))
	for _, f := range ix.Fields {
		fs := &goredis.FieldSchema{FieldName: f.Name, Sortable: f.Sortable}
		switch f.Kind {
		case Tag:
			fs.FieldType = goredis.SearchFieldTypeTag
		case Numeric:
			fs.FieldType = goredis.SearchFieldTypeNumeric
		default:
			fs.FieldType = goredis.SearchFieldTypeText
			fs.NoStem = true
			if f.Weight > 0 {
				fs.Weight = f.Weight
			}
		}
		schema = append(schema, fs)
	}
	opts := &goredis.FTCreateOptions{OnHash: true, Prefix: []interface{}{ix.Prefix}, StopWords: []interface{}{}}
	if err := c.FTCreate(ctx, ix.Name, opts, schema...).Err(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil
		}
		return fmt.Errorf("redisindex: create index=%q prefix=%q: %w", ix.Name, ix.Prefix, err)
	}
	return nil
}

func (ix *Index) key(id string) string { return ix.Prefix + id }

func (ix *Index) values(doc Doc) map[string]interface{} {
	v := map[string]interface{}{versionField: doc.Version}
	for _, f := range ix.Fields {
		switch f.Kind {
		case Tag:
			v[f.Name] = doc.Tags[f.Name]
		case Numeric:
			v[f.Name] = strconv.FormatFloat(doc.Nums[f.Name], 'f', -1, 64)
		default:
			v[f.Name] = strings.Join(searchkit.IndexWords(doc.Text[f.Name]), " ")
		}
	}
	return v
}

func (ix *Index) Upsert(ctx context.Context, id string, doc Doc) error {
	c, err := ix.client()
	if err != nil {
		return err
	}
	key := ix.key(id)
	vals := ix.values(doc)
	_, err = c.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.Del(ctx, key)
		p.HSet(ctx, key, vals)
		return nil
	})
	if err != nil {
		return fmt.Errorf("redisindex: upsert index=%q id=%q: %w", ix.Name, id, err)
	}
	return nil
}

func (ix *Index) Remove(ctx context.Context, id string) error {
	c, err := ix.client()
	if err != nil {
		return err
	}
	if err := c.Del(ctx, ix.key(id)).Err(); err != nil {
		return fmt.Errorf("redisindex: remove index=%q id=%q: %w", ix.Name, id, err)
	}
	return nil
}

func escapeTag(v string) string {
	var b strings.Builder
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('\\')
		b.WriteRune(r)
	}
	return b.String()
}

func numString(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (ix *Index) textFields(names []string) []string {
	var out []string
	if len(names) == 0 {
		for _, f := range ix.Fields {
			if f.Kind == Text {
				out = append(out, f.Name)
			}
		}
		return out
	}
	for _, n := range names {
		if f, ok := ix.field(n); ok && f.Kind == Text {
			out = append(out, n)
		}
	}
	return out
}

func (ix *Index) build(q Query) (string, *goredis.FTSearchOptions, bool) {
	words := q.Words
	if words == nil && strings.TrimSpace(q.Text) != "" {
		words = searchkit.Words(q.Text)
		if len(words) == 0 {
			return "", nil, false
		}
	}
	var parts []string
	if len(words) > 0 {
		fields := ix.textFields(q.Fields)
		if len(fields) == 0 {
			return "", nil, false
		}
		minPrefix := 2
		if q.Any {
			minPrefix = 3
		}
		terms := make([]string, 0, len(words))
		for _, w := range words {
			if utf8.RuneCountInString(w) >= minPrefix {
				terms = append(terms, w+"*")
			} else {
				terms = append(terms, w)
			}
		}
		sep := " "
		if q.Any {
			sep = " | "
		}
		parts = append(parts, "@"+strings.Join(fields, "|")+":("+strings.Join(terms, sep)+")")
	}
	tagNames := make([]string, 0, len(q.Tags))
	for n := range q.Tags {
		tagNames = append(tagNames, n)
	}
	sort.Strings(tagNames)
	for _, n := range tagNames {
		if f, ok := ix.field(n); !ok || f.Kind != Tag {
			continue
		}
		vals := make([]string, 0, len(q.Tags[n]))
		for _, v := range q.Tags[n] {
			vals = append(vals, escapeTag(v))
		}
		if len(vals) > 0 {
			parts = append(parts, "@"+n+":{"+strings.Join(vals, " | ")+"}")
		}
	}
	rangeNames := make([]string, 0, len(q.Ranges))
	for n := range q.Ranges {
		rangeNames = append(rangeNames, n)
	}
	sort.Strings(rangeNames)
	for _, n := range rangeNames {
		if f, ok := ix.field(n); !ok || f.Kind != Numeric {
			continue
		}
		r := q.Ranges[n]
		parts = append(parts, fmt.Sprintf("@%s:[%s %s]", n, numString(r[0]), numString(r[1])))
	}
	query := "*"
	if len(parts) > 0 {
		query = strings.Join(parts, " ")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	opts := &goredis.FTSearchOptions{NoContent: true, WithScores: true, LimitOffset: q.Offset, Limit: limit}
	if q.SortBy != "" {
		if f, ok := ix.field(q.SortBy); ok && f.Sortable {
			opts.SortBy = []goredis.FTSearchSortBy{{FieldName: q.SortBy, Asc: !q.Desc, Desc: q.Desc}}
		}
	}
	return query, opts, true
}

func (ix *Index) Search(ctx context.Context, q Query) (Result, error) {
	query, opts, ok := ix.build(q)
	if !ok {
		return Result{}, nil
	}
	c, err := ix.client()
	if err != nil {
		return Result{}, err
	}
	res, err := c.FTSearchWithArgs(ctx, ix.Name, query, opts).Result()
	if err != nil {
		return Result{}, fmt.Errorf("redisindex: search index=%q query=%q: %w", ix.Name, query, err)
	}
	out := Result{Total: res.Total, Hits: make([]Hit, 0, len(res.Docs))}
	for _, d := range res.Docs {
		h := Hit{ID: strings.TrimPrefix(d.ID, ix.Prefix)}
		if d.Score != nil {
			h.Score = *d.Score
		}
		out.Hits = append(out.Hits, h)
	}
	if q.MinScoreRatio > 0 && len(out.Hits) > 0 {
		top := out.Hits[0].Score
		for _, h := range out.Hits {
			if h.Score > top {
				top = h.Score
			}
		}
		kept := out.Hits[:0]
		for _, h := range out.Hits {
			if h.Score >= top*q.MinScoreRatio {
				kept = append(kept, h)
			}
		}
		out.Hits = kept
	}
	return out, nil
}

func (ix *Index) storedVersions(ctx context.Context, c *goredis.Client) (map[string]string, error) {
	have := map[string]string{}
	var cursor uint64
	for {
		keys, next, err := c.Scan(ctx, cursor, ix.Prefix+"*", 500).Result()
		if err != nil {
			return nil, fmt.Errorf("redisindex: scan prefix=%q: %w", ix.Prefix, err)
		}
		if len(keys) > 0 {
			cmds := make([]*goredis.StringCmd, len(keys))
			_, err := c.Pipelined(ctx, func(p goredis.Pipeliner) error {
				for i, k := range keys {
					cmds[i] = p.HGet(ctx, k, versionField)
				}
				return nil
			})
			if err != nil && !errors.Is(err, goredis.Nil) {
				return nil, fmt.Errorf("redisindex: read versions prefix=%q: %w", ix.Prefix, err)
			}
			for i, k := range keys {
				v, verr := cmds[i].Result()
				if verr != nil && !errors.Is(verr, goredis.Nil) {
					return nil, fmt.Errorf("redisindex: read version key=%q: %w", k, verr)
				}
				have[strings.TrimPrefix(k, ix.Prefix)] = v
			}
		}
		cursor = next
		if cursor == 0 {
			return have, nil
		}
	}
}

func (ix *Index) Reconcile(ctx context.Context, want map[string]string, load func(ids []string) (map[string]Doc, error)) (Report, error) {
	var rep Report
	c, err := ix.client()
	if err != nil {
		return rep, err
	}
	if err := ix.Ensure(ctx); err != nil {
		return rep, err
	}
	have, err := ix.storedVersions(ctx, c)
	if err != nil {
		return rep, err
	}
	var errs []error
	var stale, missing []string
	for id := range have {
		if _, ok := want[id]; !ok {
			stale = append(stale, id)
		}
	}
	for id, v := range want {
		hv, ok := have[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case hv != v:
			missing = append(missing, id)
		}
	}
	sort.Strings(stale)
	sort.Strings(missing)
	for _, id := range stale {
		if err := ix.Remove(ctx, id); err != nil {
			rep.Failed++
			errs = append(errs, err)
			continue
		}
		rep.Removed++
	}
	const batch = 200
	for start := 0; start < len(missing); start += batch {
		end := start + batch
		if end > len(missing) {
			end = len(missing)
		}
		ids := missing[start:end]
		docs, err := load(ids)
		if err != nil {
			rep.Failed += len(ids)
			errs = append(errs, fmt.Errorf("redisindex: load index=%q ids=%v: %w", ix.Name, ids, err))
			continue
		}
		for _, id := range ids {
			doc, ok := docs[id]
			if !ok {
				rep.Failed++
				errs = append(errs, fmt.Errorf("redisindex: load index=%q id=%q: document not returned", ix.Name, id))
				continue
			}
			doc.Version = want[id]
			if err := ix.Upsert(ctx, id, doc); err != nil {
				rep.Failed++
				errs = append(errs, err)
				continue
			}
			if _, existed := have[id]; existed {
				rep.Updated++
			} else {
				rep.Added++
			}
		}
	}
	return rep, errors.Join(errs...)
}
