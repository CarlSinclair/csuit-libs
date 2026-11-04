package durable

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/alicebob/miniredis/v2"
)

type recorder struct {
	mu     sync.Mutex
	bodies []string
	status int
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.bodies = append(r.bodies, req.URL.Path+" "+string(b))
	status := r.status
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
		w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found x"}`))
		return
	}
	w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func setup(t *testing.T, rec *recorder) *miniredis.Miniredis {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	prev := logger.TelegramBaseURL
	logger.TelegramBaseURL = srv.URL
	t.Cleanup(func() { logger.TelegramBaseURL = prev })

	mr := miniredis.RunT(t)
	redis.Configure(redis.Options{Settings: func() (redis.Settings, error) {
		return redis.Settings{Addr: mr.Addr(), Prefix: "w:"}, nil
	}})
	redis.Init("")
	t.Cleanup(redis.Close)
	return mr
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWorkerDeliversAndAcks(t *testing.T) {
	rec := &recorder{}
	mr := setup(t, rec)
	StartWorker(WorkerConfig{TokenFor: func(ref string) string {
		if ref == "alert" {
			return "555:alerttoken"
		}
		return "999:other"
	}})
	if err := Enqueue("alert", "42", "hello", "HTML"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "delivery", func() bool { return rec.count() == 1 })
	if !strings.HasPrefix(rec.bodies[0], "/bot555:alerttoken/sendMessage") || !strings.Contains(rec.bodies[0], `"hello"`) {
		t.Fatalf("unexpected request: %s", rec.bodies[0])
	}
	waitFor(t, "ack", func() bool {
		l, _ := mr.List("w:" + redis.ProcessingName(redis.TelegramQueueName))
		return len(l) == 0
	})
}

func TestWorkerRecoversInFlightAfterRestart(t *testing.T) {
	rec := &recorder{}
	mr := setup(t, rec)
	payload, _ := json.Marshal(redis.TelegramTask{ChatID: "7", Message: "left over", ParseMode: "HTML"})
	mr.Lpush("w:"+redis.ProcessingName(redis.TelegramQueueName), string(payload))

	StartWorker(WorkerConfig{TokenFor: func(string) string { return "1:tok" }})
	waitFor(t, "recovered delivery", func() bool { return rec.count() == 1 })
	if !strings.Contains(rec.bodies[0], "left over") {
		t.Fatalf("recovered message not sent: %s", rec.bodies[0])
	}
}

func TestWorkerDeadLettersPermanentFailureAndBadPayload(t *testing.T) {
	rec := &recorder{status: http.StatusBadRequest}
	mr := setup(t, rec)
	mr.Lpush("w:"+redis.TelegramQueueName, "not json")
	StartWorker(WorkerConfig{TokenFor: func(string) string { return "1:tok" }})
	if err := Enqueue("", "9", "doomed", "HTML"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "dead letters", func() bool {
		l, _ := mr.List("w:" + redis.TelegramDeadName)
		return len(l) == 2
	})
	l, _ := mr.List("w:" + redis.ProcessingName(redis.TelegramQueueName))
	if len(l) != 0 {
		t.Fatalf("processing list should be empty after dead-lettering, has %d", len(l))
	}
}

func TestEnqueueRejectsEmptyChat(t *testing.T) {
	if err := Enqueue("", "  ", "x", ""); err == nil {
		t.Fatal("empty chat id must be rejected")
	}
}
