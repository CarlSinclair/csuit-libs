package apikit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
	"github.com/CarlSinclair/csuit-libs/redis"
)

const (
	idempotencyTTL     = 24 * time.Hour
	idempotencyLockTTL = 2 * time.Minute
	idempotencyMaxKey  = 128
	idempotencyMaxBody = 1 << 20
	inflightMarker     = "inflight"
)

type storedResponse struct {
	Status   int    `json:"status"`
	BodyHash string `json:"body_hash"`
	Body     string `json:"body"`
}

func hashHex(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func Idempotent(h HandlerFunc) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, k *Key) {
		idem := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idem == "" {
			h(w, r, k)
			return
		}
		if len(idem) > idempotencyMaxKey {
			WriteError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long")
			return
		}
		client := redis.Client()
		if client == nil {
			logger.Warn("apikit: idempotency unavailable, redis down key_id=%d", k.ID)
			WriteError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency storage is unavailable, retry shortly")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "could not read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		bodyHash := hashHex(string(raw))
		redisKey := redis.Key("apikit:idem:" + hashHex(strconv.FormatInt(k.ID, 10), r.Method, r.URL.Path, idem))

		acquired, err := client.SetNX(redis.Ctx, redisKey, inflightMarker, idempotencyLockTTL).Result()
		if err != nil {
			logger.Error("apikit: idempotency lock failed key_id=%d path=%s err=%v", k.ID, r.URL.Path, err)
			WriteError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency storage is unavailable, retry shortly")
			return
		}
		if !acquired {
			val, gerr := client.Get(redis.Ctx, redisKey).Result()
			if gerr != nil || val == inflightMarker {
				w.Header().Set("Retry-After", "2")
				WriteError(w, http.StatusConflict, "request_in_progress", "a request with this Idempotency-Key is still being processed")
				return
			}
			var sr storedResponse
			if jerr := json.Unmarshal([]byte(val), &sr); jerr != nil {
				logger.Error("apikit: idempotency record unreadable key_id=%d path=%s err=%v", k.ID, r.URL.Path, jerr)
				WriteError(w, http.StatusConflict, "request_in_progress", "a request with this Idempotency-Key could not be replayed")
				return
			}
			if sr.BodyHash != bodyHash {
				WriteError(w, http.StatusUnprocessableEntity, "idempotency_conflict", "this Idempotency-Key was already used with a different request body")
				return
			}
			w.Header().Set("Idempotent-Replay", "true")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(sr.Status)
			if _, werr := w.Write([]byte(sr.Body)); werr != nil {
				logger.Warn("apikit: idempotent replay write failed key_id=%d err=%v", k.ID, werr)
			}
			return
		}

		rec := &statusRecorder{ResponseWriter: w, limit: idempotencyMaxBody}
		h(rec, r, k)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 500 || rec.over {
			if derr := client.Del(redis.Ctx, redisKey).Err(); derr != nil {
				logger.Warn("apikit: idempotency release failed key_id=%d path=%s err=%v", k.ID, r.URL.Path, derr)
			}
			return
		}
		payload, merr := json.Marshal(storedResponse{Status: status, BodyHash: bodyHash, Body: rec.buf.String()})
		if merr != nil {
			logger.Error("apikit: idempotency record encode failed key_id=%d err=%v", k.ID, merr)
			return
		}
		if serr := client.Set(redis.Ctx, redisKey, payload, idempotencyTTL).Err(); serr != nil {
			logger.Error("apikit: idempotency record save failed key_id=%d path=%s err=%v", k.ID, r.URL.Path, serr)
		}
	}
}
