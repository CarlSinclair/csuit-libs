package sanitizer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memSink struct {
	mu      sync.Mutex
	files   map[string]string
	aborted bool
}

func (m *memSink) Put(_ context.Context, job, name string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[job+"/"+name] = string(b)
	return err
}

func (m *memSink) Abort(_ context.Context, _ string) error { m.aborted = true; return nil }

func newReceiver(t *testing.T) (*Receiver, *Signer, *memSink, *[]Outcome) {
	t.Helper()
	sg, err := NewSigner([]byte("0123456789abcdef0123"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &memSink{files: map[string]string{}}
	var outs []Outcome
	return &Receiver{Signer: sg, Sink: sink, OnDone: func(o Outcome) { outs = append(outs, o) }}, sg, sink, &outs
}

func do(h http.Handler, method, path, tok, body string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestSignerRejectsOtherJobAndExpired(t *testing.T) {
	sg, _ := NewSigner([]byte("0123456789abcdef0123"))
	tok := sg.Token("job1", time.Minute)
	if sg.Verify(tok, "job1") != nil {
		t.Fatal("valid token rejected")
	}
	if sg.Verify(tok, "job2") == nil {
		t.Fatal("token accepted for another job")
	}
	if sg.Verify(sg.Token("job1", -time.Second), "job1") == nil {
		t.Fatal("expired token accepted")
	}
}

func TestReceiverStoresAndReports(t *testing.T) {
	rc, sg, sink, outs := newReceiver(t)
	tok := sg.Token("job1", time.Minute)
	if c := do(rc, http.MethodPut, "/job1/seg0000.ts", tok, "data"); c != http.StatusNoContent {
		t.Fatalf("put code %d", c)
	}
	if sink.files["job1/seg0000.ts"] != "data" {
		t.Fatal("file not stored")
	}
	if c := do(rc, http.MethodPut, "/job1/evil.sh", tok, "x"); c != http.StatusNotFound {
		t.Fatalf("bad name code %d", c)
	}
	if c := do(rc, http.MethodPut, "/job1/seg0001.ts", "", "x"); c != http.StatusForbidden {
		t.Fatalf("no token code %d", c)
	}
	if c := do(rc, http.MethodPost, "/job1/done", tok, `{"ok":false,"message":"boom"}`); c != http.StatusNoContent {
		t.Fatalf("done code %d", c)
	}
	if !sink.aborted || len(*outs) != 1 || (*outs)[0].OK {
		t.Fatal("failed outcome must abort partial files and be reported")
	}
}

type presignSink struct{ memSink }

func (*presignSink) PresignPut(_ context.Context, job, name string) (string, error) {
	return "https://r2.example/" + job + "/" + name + "?sig=x", nil
}

func TestReceiverPresign(t *testing.T) {
	sg, _ := NewSigner([]byte("0123456789abcdef0123"))
	rc := &Receiver{Signer: sg, Sink: &presignSink{memSink{files: map[string]string{}}}}
	tok := sg.Token("job1", time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/job1/presign/seg0000.ts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	rc.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "job1/seg0000.ts") {
		t.Fatalf("presign failed: %d %s", rec.Code, rec.Body.String())
	}
	plain, _, _, _ := newReceiver(t)
	req = httptest.NewRequest(http.MethodGet, "/job1/presign/seg0000.ts", nil)
	req.Header.Set("Authorization", "Bearer "+plain.Signer.Token("job1", time.Minute))
	rec = httptest.NewRecorder()
	plain.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("sink without presign must 404, got %d", rec.Code)
	}
}

func TestValidateJob(t *testing.T) {
	ok := Job{ID: "a1", SourceURL: "https://x/y", UploadBase: "https://x/u", CallbackURL: "https://x/c", Token: "t"}
	if validateJob(ok) != nil {
		t.Fatal("valid job rejected")
	}
	bad := ok
	bad.SourceURL = "file:///etc/passwd"
	if validateJob(bad) == nil {
		t.Fatal("non-http source accepted")
	}
}
