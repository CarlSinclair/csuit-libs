package apikit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const MaxBodyBytes = 2 << 20

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		logger.Error("apikit: encode response failed status=%d type=%T err=%v", status, v, err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"response encoding failed"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write(buf); err != nil {
		logger.Warn("apikit: write response failed status=%d err=%v", status, err)
	}
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorEnvelope{Error: ErrorBody{Code: code, Message: message}})
}

func WriteInternal(w http.ResponseWriter, what string, err error) {
	logger.Error("apikit: %s: %v", what, err)
	WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			WriteError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return false
		}
		WriteError(w, http.StatusBadRequest, "invalid_body", "could not read request body")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON for this endpoint: "+err.Error())
		return false
	}
	if dec.More() {
		WriteError(w, http.StatusBadRequest, "invalid_json", "request body must contain a single JSON object")
		return false
	}
	return true
}

type Page struct {
	Limit  int
	Cursor int64
}

func ParsePage(w http.ResponseWriter, r *http.Request, defaultLimit, maxLimit int) (Page, bool) {
	p := Page{Limit: defaultLimit}
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			WriteError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return p, false
		}
		if n > maxLimit {
			n = maxLimit
		}
		p.Limit = n
	}
	if v := strings.TrimSpace(q.Get("cursor")); v != "" {
		id, err := DecodeCursor(v)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is not valid")
			return p, false
		}
		p.Cursor = id
	}
	return p, true
}

func EncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func DecodeCursor(s string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 0 {
		return 0, errors.New("invalid cursor")
	}
	return id, nil
}

type List struct {
	Data       any    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func WriteList(w http.ResponseWriter, data any, nextID int64) {
	out := List{Data: data}
	if nextID > 0 {
		out.NextCursor = EncodeCursor(nextID)
	}
	WriteJSON(w, http.StatusOK, out)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	limit  int
	over   bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	if s.limit > 0 && !s.over {
		if s.buf.Len()+len(b) > s.limit {
			s.over = true
		} else {
			s.buf.Write(b)
		}
	}
	return s.ResponseWriter.Write(b)
}
