package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendHTMLAndPhotoPayloads(t *testing.T) {
	var method string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		payload = nil
		json.Unmarshal(raw, &payload)
		method = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Write([]byte(`{"ok":true,"result":{"message_id":7}}`))
	}))
	defer srv.Close()
	b := New("123456:ABC-token", "test_bot", "s")
	b.API = srv.URL

	if err := b.SendHTML(context.Background(), "42", "<b>hi</b>"); err != nil {
		t.Fatalf("SendHTML: %v", err)
	}
	if method != "sendMessage" || payload["parse_mode"] != "HTML" || payload["text"] != "<b>hi</b>" || payload["disable_web_page_preview"] != true {
		t.Fatalf("sendMessage payload = %s %+v", method, payload)
	}
	if err := b.SendPhotoHTML(context.Background(), "42", "https://x.example/p.jpg", "<b>cap</b>"); err != nil {
		t.Fatalf("SendPhotoHTML: %v", err)
	}
	if method != "sendPhoto" || payload["photo"] != "https://x.example/p.jpg" || payload["caption"] != "<b>cap</b>" || payload["parse_mode"] != "HTML" {
		t.Fatalf("sendPhoto payload = %s %+v", method, payload)
	}
}

func TestSendPhotoHTMLRejectsLongCaptionWithoutCalling(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	b := New("123456:ABC-token", "test_bot", "s")
	b.API = srv.URL
	if err := b.SendPhotoHTML(context.Background(), "42", "https://x.example/p.jpg", strings.Repeat("a", MaxCaption+1)); err == nil || called {
		t.Fatalf("long caption: err=%v called=%v", err, called)
	}
}

func TestSendPhotoHTMLReportsBlockedChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
	}))
	defer srv.Close()
	b := New("123456:ABC-token", "test_bot", "s")
	b.API = srv.URL
	if err := b.SendPhotoHTML(context.Background(), "42", "https://x.example/p.jpg", "c"); !errors.Is(err, ErrBotBlocked) {
		t.Fatalf("err = %v, want ErrBotBlocked", err)
	}
}
