package httpkit

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	MaxBodyBytes      int64 = 10 << 20
	MaxMultipartBytes int64 = 32 << 20
)

func WriteJSONBody(w http.ResponseWriter, r *http.Request, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		path := ""
		if r != nil {
			path = r.URL.Path
		}
		logger.Warn("response body encode failed path=%s type=%T err=%v", path, v, err)
	}
}

func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			limit := MaxBodyBytes
			if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
				limit = MaxMultipartBytes
			}
			if r.ContentLength > limit {
				logger.Warn("request body too large method=%s path=%s length=%d limit=%d remote=%s", r.Method, r.URL.Path, r.ContentLength, limit, r.RemoteAddr)
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
