package sanitizer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Job is everything a video worker needs, expressed as URLs so the worker can
// run anywhere: a GitHub runner, this host, or a managed service. The worker
// reads SourceURL, PUTs each output file to UploadBase+"/"+name, and POSTs the
// outcome to CallbackURL. Token authorizes the uploads and the callback.
type Job struct {
	ID          string
	SourceURL   string
	UploadBase  string
	CallbackURL string
	Token       string
	MaxSeconds  int
	// Mode is ModeHLS (default) or ModeMP4, a single sanitized MP4 named out.mp4.
	Mode string
}

const (
	ModeHLS = "hls"
	ModeMP4 = "mp4"
)

// Backend starts a Job somewhere. Submit returns once the job is accepted, not
// when it finishes; the outcome arrives at the Receiver.
type Backend interface {
	Name() string
	Submit(ctx context.Context, job Job) error
}

// Outcome is what a worker reports when a job ends.
type Outcome struct {
	JobID   string
	OK      bool
	Message string
	Master  string
}

// Signer issues and checks per-job tokens, so a leaked token only ever allows
// writing one job's output until it expires.
type Signer struct{ secret []byte }

func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 16 {
		return nil, errors.New("video signer secret must be at least 16 bytes")
	}
	return &Signer{secret: secret}, nil
}

func (s *Signer) mac(jobID string, exp int64) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(jobID + "|" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Signer) Token(jobID string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	raw := strconv.FormatInt(exp, 10) + "." + s.mac(jobID, exp)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func (s *Signer) Verify(token, jobID string) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return errors.New("malformed token")
	}
	expStr, sig, ok := strings.Cut(string(raw), ".")
	if !ok {
		return errors.New("malformed token")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return errors.New("token expired")
	}
	if !hmac.Equal([]byte(sig), []byte(s.mac(jobID, exp))) {
		return errors.New("bad token")
	}
	return nil
}

// Sink stores the files a worker uploads and removes partial output.
type Sink interface {
	Put(ctx context.Context, jobID, name string, r io.Reader, size int64) error
	Abort(ctx context.Context, jobID string) error
}

// Presigner is an optional Sink capability: when present, workers are handed a
// URL to PUT each file to directly, and no output bytes pass through the app.
type Presigner interface {
	PresignPut(ctx context.Context, jobID, name string) (string, error)
}

var (
	jobIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}\.(ts|m3u8|mp4)$`)
)

// maxFileBytes bounds one uploaded segment or playlist.
const maxFileBytes = 4 << 30

// Receiver is the HTTP side of the contract. Mount it under a prefix with
// http.StripPrefix so it sees /{jobID}/{name} for PUT and /{jobID}/done for POST.
type Receiver struct {
	Signer *Signer
	Sink   Sink
	OnDone func(Outcome)
}

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || len(parts) > 3 || !jobIDRe.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	jobID, last := parts[0], parts[1]
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := rc.Signer.Verify(tok, jobID); err != nil {
		logger.Warn("video receiver: refused %s %s for job %s: %v", r.Method, r.URL.Path, jobID, err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if len(parts) == 3 {
		rc.presign(w, r, jobID, parts)
		return
	}
	switch {
	case r.Method == http.MethodPut && nameRe.MatchString(last):
		body := http.MaxBytesReader(w, r.Body, maxFileBytes)
		if err := rc.Sink.Put(r.Context(), jobID, last, body, r.ContentLength); err != nil {
			logger.Error("video receiver: storing %s for job %s failed: %v", last, jobID, err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && last == "done":
		var in struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
			Master  string `json:"master"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			logger.Error("video receiver: bad outcome body for job %s: %v", jobID, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		out := Outcome{JobID: jobID, OK: in.OK, Message: in.Message, Master: in.Master}
		if !out.OK {
			if err := rc.Sink.Abort(r.Context(), jobID); err != nil {
				logger.Error("video receiver: job %s failed and its partial files could not be removed: %v", jobID, err)
			}
		}
		if rc.OnDone != nil {
			rc.OnDone(out)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (rc *Receiver) presign(w http.ResponseWriter, r *http.Request, jobID string, parts []string) {
	p, ok := rc.Sink.(Presigner)
	if parts[1] != "presign" || r.Method != http.MethodGet || !nameRe.MatchString(parts[2]) || !ok {
		http.NotFound(w, r)
		return
	}
	u, err := p.PresignPut(r.Context(), jobID, parts[2])
	if err != nil {
		logger.Error("video receiver: presigning %s for job %s failed: %v", parts[2], jobID, err)
		http.Error(w, "presign failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, u)
}

func jobEnv(j Job) []string {
	return []string{
		"SOURCE_URL=" + j.SourceURL,
		"UPLOAD_BASE=" + j.UploadBase,
		"CALLBACK_URL=" + j.CallbackURL,
		"TOKEN=" + j.Token,
		"MAX_SECONDS=" + strconv.Itoa(j.MaxSeconds),
		"MODE=" + modeOf(j),
	}
}

func modeOf(j Job) string {
	if j.Mode == ModeMP4 {
		return ModeMP4
	}
	return ModeHLS
}

func validateJob(j Job) error {
	if !jobIDRe.MatchString(j.ID) {
		return fmt.Errorf("invalid job id %q", j.ID)
	}
	for _, u := range []string{j.SourceURL, j.UploadBase, j.CallbackURL} {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return fmt.Errorf("job %s has a non-http url", j.ID)
		}
	}
	if j.Mode != "" && j.Mode != ModeHLS && j.Mode != ModeMP4 {
		return fmt.Errorf("job %s has unknown mode %q", j.ID, j.Mode)
	}
	if j.Token == "" {
		return fmt.Errorf("job %s has no token", j.ID)
	}
	return nil
}
