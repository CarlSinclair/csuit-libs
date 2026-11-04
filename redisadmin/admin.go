package redisadmin

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
	goredis "github.com/redis/go-redis/v9"
)

const DefaultPageSize = 10

type KeySummary struct {
	Name string
	Type string
	Size int64
}

type Pager struct {
	Total int64
	Page  int
	Pages int
	Prev  int
	Next  int
}

func AtoiDefault(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func ClampPage(p int) int {
	if p < 1 {
		return 1
	}
	return p
}

func NewPager(total int64, page, pageSize int) Pager {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	page = ClampPage(page)
	pages := 1
	if total > 0 {
		pages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	if page > pages {
		page = pages
	}
	p := Pager{Total: total, Page: page, Pages: pages, Prev: page - 1, Next: page + 1}
	if p.Prev < 1 {
		p.Prev = 1
	}
	if p.Next > pages {
		p.Next = pages
	}
	return p
}

func bounds(page, pageSize int) (int64, int64) {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	start := int64((ClampPage(page) - 1) * pageSize)
	return start, start + int64(pageSize) - 1
}

func client() (*goredis.Client, error) {
	c := redis.Client()
	if c == nil {
		return nil, fmt.Errorf("redis is not connected")
	}
	return c, nil
}

func ListLen(queue string) (int64, error) {
	c, err := client()
	if err != nil {
		return 0, err
	}
	return c.LLen(redis.Ctx, redis.Key(queue)).Result()
}

func SetLen(queue string) (int64, error) {
	c, err := client()
	if err != nil {
		return 0, err
	}
	return c.ZCard(redis.Ctx, redis.Key(queue)).Result()
}

func decodeTask(raw string) redis.EmailTask {
	var t redis.EmailTask
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return redis.EmailTask{ToEmail: "N/A", Subject: "Error: Malformed JSON", RawJSON: raw}
	}
	t.RawJSON = raw
	return t
}

func EmailTasks(queue string, page, pageSize int) ([]redis.EmailTask, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	start, stop := bounds(page, pageSize)
	items, err := c.LRange(redis.Ctx, redis.Key(queue), start, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]redis.EmailTask, 0, len(items))
	for _, raw := range items {
		out = append(out, decodeTask(raw))
	}
	return out, nil
}

func RetryTasks(page, pageSize int) ([]redis.EmailTask, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	start, stop := bounds(page, pageSize)
	items, err := c.ZRange(redis.Ctx, redis.Key(redis.EmailQueueRetryName), start, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]redis.EmailTask, 0, len(items))
	for _, raw := range items {
		out = append(out, decodeTask(raw))
	}
	return out, nil
}

func SentRecords(page, pageSize int) ([]redis.EmailSentRecord, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	start, stop := bounds(page, pageSize)
	items, err := c.LRange(redis.Ctx, redis.Key(redis.EmailSentLogName), start, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]redis.EmailSentRecord, 0, len(items))
	for _, raw := range items {
		var rec redis.EmailSentRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			logger.Error("redisadmin: malformed sent record raw=%s err=%v", raw, err)
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func ErrorRecords(page, pageSize int) ([]redis.EmailErrorRecord, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	start, stop := bounds(page, pageSize)
	items, err := c.LRange(redis.Ctx, redis.Key(redis.EmailErrorLogName), start, stop).Result()
	if err != nil {
		return nil, err
	}
	out := make([]redis.EmailErrorRecord, 0, len(items))
	for _, raw := range items {
		var rec redis.EmailErrorRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			logger.Error("redisadmin: malformed error record raw=%s err=%v", raw, err)
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func LogLen(name string) (int64, error) { return ListLen(name) }

func Pattern(prefixless string) string {
	return redis.CurrentPrefix() + prefixless
}

func Summary(key string) KeySummary {
	c, err := client()
	if err != nil {
		return KeySummary{Name: key, Type: "unknown", Size: -1}
	}
	typ, err := c.Type(redis.Ctx, key).Result()
	if err != nil {
		logger.Error("redisadmin: key type failed key=%q err=%v", key, err)
		return KeySummary{Name: key, Type: "unknown", Size: -1}
	}
	s := KeySummary{Name: key, Type: typ, Size: -1}
	var size int64
	var sizeErr error
	switch typ {
	case "list":
		size, sizeErr = c.LLen(redis.Ctx, key).Result()
	case "set":
		size, sizeErr = c.SCard(redis.Ctx, key).Result()
	case "zset":
		size, sizeErr = c.ZCard(redis.Ctx, key).Result()
	case "hash":
		size, sizeErr = c.HLen(redis.Ctx, key).Result()
	case "string":
		size, sizeErr = c.StrLen(redis.Ctx, key).Result()
	default:
		return s
	}
	if sizeErr != nil {
		logger.Error("redisadmin: key size failed key=%q type=%s err=%v", key, typ, sizeErr)
		return s
	}
	s.Size = size
	return s
}

func KeysPage(pattern string, page, pageSize int) ([]KeySummary, int, error) {
	c, err := client()
	if err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	page = ClampPage(page)
	var all []string
	var cursor uint64
	for {
		keys, next, err := c.Scan(redis.Ctx, cursor, pattern, 500).Result()
		if err != nil {
			return nil, len(all), err
		}
		all = append(all, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(all)
	total := len(all)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]KeySummary, 0, end-start)
	for _, k := range all[start:end] {
		out = append(out, Summary(k))
	}
	return out, total, nil
}

func ScanKeys(pattern string, startCursor uint64, want int) ([]KeySummary, uint64, error) {
	c, err := client()
	if err != nil {
		return nil, 0, err
	}
	var out []KeySummary
	cursor := startCursor
	for {
		keys, next, err := c.Scan(redis.Ctx, cursor, pattern, 500).Result()
		if err != nil {
			return out, cursor, err
		}
		for _, k := range keys {
			out = append(out, Summary(k))
			if len(out) >= want {
				return out, next, nil
			}
		}
		cursor = next
		if cursor == 0 {
			return out, 0, nil
		}
	}
}

func Inspect(queue string, limit int64) ([]string, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	return c.LRange(redis.Ctx, redis.Key(queue), 0, limit).Result()
}

func ClearQueues(queues ...string) error {
	c, err := client()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(queues))
	for _, q := range queues {
		keys = append(keys, redis.Key(q))
	}
	return c.Del(redis.Ctx, keys...).Err()
}

func DeleteItem(queue, raw string) error {
	c, err := client()
	if err != nil {
		return err
	}
	if queue == redis.EmailQueueRetryName {
		return c.ZRem(redis.Ctx, redis.Key(queue), raw).Err()
	}
	return c.LRem(redis.Ctx, redis.Key(queue), 1, raw).Err()
}

func MoveItem(from, to, raw string) error {
	c, err := client()
	if err != nil {
		return err
	}
	toKey, fromKey := redis.Key(to), redis.Key(from)
	if err := c.LPush(redis.Ctx, toKey, raw).Err(); err != nil {
		return fmt.Errorf("pushing to %s: %w", toKey, err)
	}
	var rerr error
	if from == redis.EmailQueueRetryName {
		rerr = c.ZRem(redis.Ctx, fromKey, raw).Err()
	} else {
		rerr = c.LRem(redis.Ctx, fromKey, 1, raw).Err()
	}
	if rerr != nil {
		if undo := c.LRem(redis.Ctx, toKey, 1, raw).Err(); undo != nil {
			logger.Error("redisadmin: undo push failed key=%s err=%v", toKey, undo)
		}
		return fmt.Errorf("removing from %s: %w", fromKey, rerr)
	}
	return nil
}

func ResendTask(rawTask string) error {
	var task redis.EmailTask
	if err := json.Unmarshal([]byte(rawTask), &task); err != nil {
		return fmt.Errorf("invalid task json: %w", err)
	}
	task.Attempts = 0
	return redis.Enqueue(redis.EmailQueueName, task)
}

func RunOne(send func(redis.EmailTask) error) (bool, error) {
	c, err := client()
	if err != nil {
		return false, err
	}
	val, err := c.RPop(redis.Ctx, redis.Key(redis.EmailQueueName)).Bytes()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	deadKey := redis.Key(redis.EmailQueueDeadName)
	var task redis.EmailTask
	if uerr := json.Unmarshal(val, &task); uerr != nil {
		if perr := c.LPush(redis.Ctx, deadKey, val).Err(); perr != nil {
			logger.Error("redisadmin: run_one could not dead-letter undecodable item err=%v raw=%s", perr, string(val))
		}
		return true, fmt.Errorf("undecodable task: %w", uerr)
	}
	if serr := send(task); serr != nil {
		redis.RecordEmailError(task, task.Attempts+1, serr)
		if perr := c.LPush(redis.Ctx, deadKey, val).Err(); perr != nil {
			logger.Error("redisadmin: run_one could not dead-letter failed task to=%q err=%v", task.ToEmail, perr)
		}
		return true, serr
	}
	redis.RecordEmailSent(task, task.Attempts+1)
	return true, nil
}
