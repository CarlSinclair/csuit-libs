package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendMessagePacesAndRetriesRateLimit(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	b := New("123456:ABC-token", "tvt_bot", "s")
	b.API = srv.URL
	start := time.Now()
	if err := b.SendMessage(context.Background(), "42", "hi"); err != nil {
		t.Fatalf("send after rate limit: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", calls)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("did not wait for retry_after")
	}
	if err := b.SendMessage(context.Background(), "42", "again"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < perChatGap {
		t.Fatalf("two messages to one chat were not spaced out")
	}
}
