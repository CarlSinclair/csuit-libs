package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRetriesTransientWithoutBlockingOtherBots(t *testing.T) {
	var flaky int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/bot111:aaa/sendMessage" && atomic.AddInt32(&flaky, 1) == 1 {
			w.Write([]byte(`{"ok":false,"error_code":500,"description":"boom"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	For("111:aaa").API = srv.URL
	For("222:bbb").API = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	slow := make(chan error, 1)
	go func() {
		slow <- Do(ctx, "111:aaa", "test", func(ctx context.Context, b *Bot) error {
			return b.SendWithMode(ctx, "1", "hi", "")
		})
	}()
	time.Sleep(300 * time.Millisecond)

	start := time.Now()
	if err := Do(ctx, "222:bbb", "test", func(ctx context.Context, b *Bot) error {
		return b.SendWithMode(ctx, "1", "hi", "")
	}); err != nil {
		t.Fatalf("other bot send failed: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("other bot was held up for %s", time.Since(start))
	}
	if err := <-slow; err != nil {
		t.Fatalf("retried send failed: %v", err)
	}
	if got := atomic.LoadInt32(&flaky); got != 2 {
		t.Fatalf("expected 2 attempts on the flaky bot, got %d", got)
	}
}
