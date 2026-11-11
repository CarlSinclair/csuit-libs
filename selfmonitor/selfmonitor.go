package selfmonitor

import (
	"encoding/json"
	"net"
	"net/http"
	"runtime"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Snapshot struct {
	Service       string             `json:"service"`
	HeapAlloc     uint64             `json:"heap_alloc"`
	HeapSys       uint64             `json:"heap_sys"`
	HeapObjects   uint64             `json:"heap_objects"`
	StackSys      uint64             `json:"stack_sys"`
	Goroutines    int                `json:"goroutines"`
	GCNum         uint32             `json:"gc_num"`
	GCPauseLastNs uint64             `json:"gc_pause_last_ns"`
	GCCPUFraction float64            `json:"gc_cpu_fraction"`
	NextGC        uint64             `json:"next_gc"`
	Extra         map[string]float64 `json:"extra,omitempty"`
}

func Self(service string, extra func() map[string]float64) Snapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := Snapshot{
		Service:       service,
		HeapAlloc:     ms.HeapAlloc,
		HeapSys:       ms.HeapSys,
		HeapObjects:   ms.HeapObjects,
		StackSys:      ms.StackSys,
		Goroutines:    runtime.NumGoroutine(),
		GCNum:         ms.NumGC,
		GCPauseLastNs: ms.PauseNs[(ms.NumGC+255)%256],
		GCCPUFraction: ms.GCCPUFraction,
		NextGC:        ms.NextGC,
	}
	if extra != nil {
		s.Extra = extra()
	}
	return s
}

// Allowed keeps the endpoint reachable only from a direct loopback caller. A
// request proxied by Caddy carries X-Forwarded-For and is refused, so mounting
// the route on a public-facing listener does not expose it.
func Allowed(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

// Handler returns the GET /internal/monitor handler for a service. extra may
// be nil.
func Handler(service string, extra func() map[string]float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(r) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(Self(service, extra)); err != nil {
			logger.Error("selfmonitor: encode failed service=%s: %v", service, err)
		}
	}
}
