package redisadmin

import (
	"testing"

	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/alicebob/miniredis/v2"
)

func setup(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	redis.Configure(redis.Options{Settings: func() (redis.Settings, error) {
		return redis.Settings{Addr: mr.Addr(), Prefix: "a:"}, nil
	}})
	redis.Init("")
	t.Cleanup(redis.Close)
	return mr
}

func TestPagerAndHelpers(t *testing.T) {
	p := NewPager(95, 3, 20)
	if p.Page != 3 {
		t.Fatalf("page=%d", p.Page)
	}
	if got := AtoiDefault("x", 5); got != 5 {
		t.Fatalf("AtoiDefault fallback=%d", got)
	}
	if got := AtoiDefault("12", 5); got != 12 {
		t.Fatalf("AtoiDefault parse=%d", got)
	}
	if ClampPage(0) < 1 {
		t.Fatal("ClampPage must be at least 1")
	}
}

func TestQueueListingMoveDeleteClear(t *testing.T) {
	setup(t)
	for _, to := range []string{"a@x", "b@x", "c@x"} {
		if err := redis.Enqueue(redis.EmailQueueName, redis.EmailTask{ToEmail: to}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := ListLen(redis.EmailQueueName)
	if err != nil || n != 3 {
		t.Fatalf("len=%d err=%v", n, err)
	}
	tasks, err := EmailTasks(redis.EmailQueueName, 1, 2)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("page 1 tasks=%d err=%v", len(tasks), err)
	}
	items, err := Inspect(redis.EmailQueueName, 10)
	if err != nil || len(items) != 3 {
		t.Fatalf("inspect=%d err=%v", len(items), err)
	}
	if err := MoveItem(redis.EmailQueueName, redis.EmailQueueDeadName, items[0]); err != nil {
		t.Fatal(err)
	}
	if n, _ := ListLen(redis.EmailQueueDeadName); n != 1 {
		t.Fatalf("dead len=%d", n)
	}
	if n, _ := ListLen(redis.EmailQueueName); n != 2 {
		t.Fatalf("source len after move=%d", n)
	}
	if err := DeleteItem(redis.EmailQueueDeadName, items[0]); err != nil {
		t.Fatal(err)
	}
	if n, _ := ListLen(redis.EmailQueueDeadName); n != 0 {
		t.Fatalf("dead len after delete=%d", n)
	}
	if err := ClearQueues(redis.EmailQueueName); err != nil {
		t.Fatal(err)
	}
	if n, _ := ListLen(redis.EmailQueueName); n != 0 {
		t.Fatalf("len after clear=%d", n)
	}
}
