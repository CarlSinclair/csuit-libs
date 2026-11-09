package cookiejar

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"time"
)

const maxEncodedBytes = 3800

type Config struct {
	ShortName string
	LongName  string
	Short     []string
	Long      []string
	Logf      func(format string, args ...interface{})
	NoCookies func(r *http.Request) bool
}

type entry struct {
	V string `json:"v"`
	E int64  `json:"e,omitempty"`
}

type jar map[string]entry

type middleware struct {
	cfg   Config
	long  map[string]bool
	known map[string]bool
}

func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.ShortName == "" {
		cfg.ShortName = "__Host-session"
	}
	if cfg.LongName == "" {
		cfg.LongName = "__Host-persistent"
	}
	m := &middleware{cfg: cfg, long: map[string]bool{}, known: map[string]bool{}}
	for _, n := range cfg.Long {
		m.long[n] = true
		m.known[n] = true
	}
	for _, n := range cfg.Short {
		m.known[n] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.serve(next, w, r)
		})
	}
}

func (m *middleware) isLong(name string) bool { return m.long[name] }

func (m *middleware) target(short, long jar, name string) jar {
	if m.isLong(name) {
		return long
	}
	return short
}

func (m *middleware) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	var shortRaw, longRaw string
	legacy := map[string]string{}
	var passthrough []*http.Cookie
	for _, c := range r.Cookies() {
		switch {
		case c.Name == m.cfg.ShortName:
			shortRaw = c.Value
		case c.Name == m.cfg.LongName:
			longRaw = c.Value
		case m.known[c.Name]:
			legacy[c.Name] = c.Value
		default:
			passthrough = append(passthrough, c)
		}
	}
	short := decode(shortRaw, now, func(n string) bool { return !m.isLong(n) })
	long := decode(longRaw, now, m.isLong)
	origShort, origLong := encode(short), encode(long)
	for name, val := range legacy {
		if !validValue(val) {
			continue
		}
		j := m.target(short, long, name)
		if _, ok := j[name]; !ok {
			j[name] = entry{V: val}
		}
	}

	r.Header.Del("Cookie")
	for _, c := range passthrough {
		r.AddCookie(c)
	}
	for _, j := range []jar{short, long} {
		for _, name := range sortedKeys(j) {
			r.AddCookie(&http.Cookie{Name: name, Value: j[name].V})
		}
	}

	cw := &writer{
		ResponseWriter: w,
		m:              m,
		now:            now,
		short:          short,
		long:           long,
		origShort:      origShort,
		origLong:       origLong,
		hadShort:       shortRaw != "",
		hadLong:        longRaw != "",
		migrated:       len(legacy) > 0,
		suppress:       m.cfg.NoCookies != nil && m.cfg.NoCookies(r),
	}
	for name := range legacy {
		cw.expire = append(cw.expire, name)
	}
	sort.Strings(cw.expire)
	next.ServeHTTP(cw, r)
	cw.finalize()
}

type writer struct {
	http.ResponseWriter
	m         *middleware
	now       time.Time
	short     jar
	long      jar
	origShort string
	origLong  string
	hadShort  bool
	hadLong   bool
	migrated  bool
	suppress  bool
	expire    []string
	done      bool
}

func (w *writer) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.finalize()
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(b []byte) (int, error) {
	w.finalize()
	return w.ResponseWriter.Write(b)
}

func (w *writer) Flush() {
	w.finalize()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("cookiejar: underlying ResponseWriter does not support hijacking")
	}
	return h.Hijack()
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *writer) finalize() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	if w.suppress {
		h.Del("Set-Cookie")
		return
	}
	changed := w.migrated
	if lines := h.Values("Set-Cookie"); len(lines) > 0 {
		keep := make([]string, 0, len(lines))
		for _, line := range lines {
			c := parseSetCookie(line)
			if c == nil || c.Domain != "" || (c.Path != "" && c.Path != "/") ||
				c.Name == w.m.cfg.ShortName || c.Name == w.m.cfg.LongName || !validName(c.Name) {
				keep = append(keep, line)
				continue
			}
			deleting := c.MaxAge < 0 || (!c.Expires.IsZero() && !c.Expires.After(w.now))
			if !deleting && !validValue(c.Value) {
				keep = append(keep, line)
				continue
			}
			j := w.m.target(w.short, w.long, c.Name)
			if deleting {
				delete(j, c.Name)
				continue
			}
			e := entry{V: c.Value}
			if c.MaxAge > 0 {
				e.E = w.now.Add(time.Duration(c.MaxAge) * time.Second).Unix()
			} else if !c.Expires.IsZero() {
				e.E = c.Expires.Unix()
			}
			if old, ok := j[c.Name]; ok && old.V == e.V && e.E <= old.E+3600 && (old.E != 0 || e.E == 0) {
				continue
			}
			j[c.Name] = e
		}
		h.Del("Set-Cookie")
		for _, l := range keep {
			h.Add("Set-Cookie", l)
		}
	}
	if encode(w.short) != w.origShort || encode(w.long) != w.origLong {
		changed = true
	}
	if changed {
		if encode(w.short) != w.origShort || w.migrated {
			w.emit(w.m.cfg.ShortName, w.short, w.hadShort)
		}
		if encode(w.long) != w.origLong || w.migrated {
			w.emit(w.m.cfg.LongName, w.long, w.hadLong)
		}
	}
	for _, name := range w.expire {
		h.Add("Set-Cookie", expired(name))
	}
}

func (w *writer) emit(name string, j jar, had bool) {
	h := w.Header()
	if len(j) == 0 {
		if had {
			h.Add("Set-Cookie", expired(name))
		}
		return
	}
	val := encode(j)
	if len(val) > maxEncodedBytes && w.m.cfg.Logf != nil {
		w.m.cfg.Logf("cookiejar: %s cookie is %d bytes, browsers may reject it", name, len(val))
	}
	c := &http.Cookie{Name: name, Value: val, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if exp := maxExpiry(j); exp > 0 && name != w.m.cfg.ShortName {
		t := time.Unix(exp, 0)
		secs := int(t.Sub(w.now).Seconds())
		if secs < 1 {
			secs = 1
		}
		c.Expires = t.UTC()
		c.MaxAge = secs
	}
	h.Add("Set-Cookie", c.String())
}

func expired(name string) string {
	return (&http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}).String()
}

func maxExpiry(j jar) int64 {
	var max int64
	for _, e := range j {
		if e.E > max {
			max = e.E
		}
	}
	return max
}

func sortedKeys(j jar) []string {
	keys := make([]string, 0, len(j))
	for k := range j {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseSetCookie(line string) *http.Cookie {
	cs := (&http.Response{Header: http.Header{"Set-Cookie": {line}}}).Cookies()
	if len(cs) == 0 {
		return nil
	}
	return cs[0]
}

func encode(j jar) string {
	if len(j) == 0 {
		return ""
	}
	b, err := json.Marshal(map[string]entry(j))
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decode(raw string, now time.Time, keep func(string) bool) jar {
	out := jar{}
	if raw == "" {
		return out
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return out
	}
	var m map[string]entry
	if json.Unmarshal(b, &m) != nil {
		return out
	}
	for k, e := range m {
		if !validName(k) || !validValue(e.V) || !keep(k) {
			continue
		}
		if e.E != 0 && e.E <= now.Unix() {
			continue
		}
		out[k] = e
	}
	return out
}

func validName(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validValue(v string) bool {
	if len(v) > 2048 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 0x21 || c > 0x7e || c == '"' || c == ',' || c == ';' || c == '\\' {
			return false
		}
	}
	return true
}
