package redis

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func start(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	Configure(Options{Settings: func() (Settings, error) {
		return Settings{Addr: mr.Addr(), Prefix: "t:"}, nil
	}})
	Init("")
	if Client() == nil {
		t.Fatal("redis client not connected")
	}
	t.Cleanup(Close)
	return mr
}

func TestEnqueueDequeueFIFOAndPrefix(t *testing.T) {
	mr := start(t)
	for _, to := range []string{"a@x", "b@x"} {
		if err := Enqueue("q", EmailTask{ToEmail: to}); err != nil {
			t.Fatal(err)
		}
	}
	if !mr.Exists("t:q") {
		t.Fatal("queue key is not prefixed")
	}
	for _, want := range []string{"a@x", "b@x"} {
		raw, ok, err := Dequeue("q", time.Second)
		if err != nil || !ok {
			t.Fatalf("dequeue: ok=%v err=%v", ok, err)
		}
		var got EmailTask
		if err := json.Unmarshal(raw, &got); err != nil || got.ToEmail != want {
			t.Fatalf("order: got %q want %q err=%v", got.ToEmail, want, err)
		}
	}
}

func TestReliableQueueAckAndRecover(t *testing.T) {
	mr := start(t)
	if err := Enqueue("rq", EmailTask{ToEmail: "one@x"}); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue("rq", EmailTask{ToEmail: "two@x"}); err != nil {
		t.Fatal(err)
	}
	first, ok, err := DequeueReliable("rq", time.Second)
	if err != nil || !ok {
		t.Fatalf("dequeue: ok=%v err=%v", ok, err)
	}
	second, ok, err := DequeueReliable("rq", time.Second)
	if err != nil || !ok {
		t.Fatalf("dequeue 2: ok=%v err=%v", ok, err)
	}
	if n, _ := mr.List("t:" + ProcessingName("rq")); len(n) != 2 {
		t.Fatalf("processing list has %d items, want 2", len(n))
	}
	if err := Ack("rq", first); err != nil {
		t.Fatal(err)
	}
	if err := Ack("rq", first); err == nil {
		t.Fatal("second ack of the same item should report it missing")
	}
	n, err := RecoverProcessing("rq")
	if err != nil || n != 1 {
		t.Fatalf("recover: n=%d err=%v", n, err)
	}
	raw, ok, err := DequeueReliable("rq", time.Second)
	if err != nil || !ok || string(raw) != string(second) {
		t.Fatalf("recovered item mismatch ok=%v err=%v", ok, err)
	}
}

func TestAllowLimitsPerKey(t *testing.T) {
	start(t)
	for i := 0; i < 2; i++ {
		ok, _, err := Allow("login:1.2.3.4", 2, time.Minute)
		if err != nil || !ok {
			t.Fatalf("request %d should pass: ok=%v err=%v", i+1, ok, err)
		}
	}
	ok, retry, err := Allow("login:1.2.3.4", 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok || retry < 1 {
		t.Fatalf("third request should be limited: ok=%v retry=%d", ok, retry)
	}
	if ok, _, _ := Allow("login:5.6.7.8", 2, time.Minute); !ok {
		t.Fatal("a different key must have its own budget")
	}
}

func TestAllowFailsOpenWithoutRedis(t *testing.T) {
	ok, _, err := Allow("k", 1, time.Minute)
	if !ok || err != nil {
		t.Fatalf("limiter must allow when redis is unavailable: ok=%v err=%v", ok, err)
	}
}

func TestSpoolReplaysIntoRedis(t *testing.T) {
	mr := start(t)
	if err := spoolAppend("spooled", []byte(`{"to_email":"s@x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := flushSpool(); err != nil {
		t.Fatal(err)
	}
	items, err := mr.List("t:spooled")
	if err != nil || len(items) != 1 {
		t.Fatalf("replayed items=%d err=%v", len(items), err)
	}
}

func TestEmailTaskFailureRouting(t *testing.T) {
	mr := start(t)
	task := EmailTask{ToEmail: "w@x", TemplateName: "welcome"}
	payload, _ := json.Marshal(task)

	processEmailTask(task, payload, func(EmailTask) error { return nil }, nil)
	if mr.Exists("t:"+EmailQueueRetryName) || mr.Exists("t:"+EmailQueueDeadName) {
		t.Fatal("a successful send must not retry or dead-letter")
	}

	boom := errors.New("temporary smtp failure")
	processEmailTask(task, payload, func(EmailTask) error { return boom }, func(error) bool { return false })
	members, err := mr.ZMembers("t:" + EmailQueueRetryName)
	if err != nil || len(members) != 1 {
		t.Fatalf("transient failure should schedule one retry: members=%d err=%v", len(members), err)
	}
	var retried EmailTask
	if err := json.Unmarshal([]byte(members[0]), &retried); err != nil || retried.Attempts != 1 {
		t.Fatalf("retry attempts=%d err=%v", retried.Attempts, err)
	}

	processEmailTask(task, payload, func(EmailTask) error { return boom }, func(error) bool { return true })
	dead, err := mr.List("t:" + EmailQueueDeadName)
	if err != nil || len(dead) != 1 {
		t.Fatalf("permanent failure should dead-letter: dead=%d err=%v", len(dead), err)
	}
}
