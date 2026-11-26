package redis

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	outageCheckInterval  = 5 * time.Second
	outagePingTimeout    = 3 * time.Second
	outageFailuresToDown = 2
	outageLoadingGrace   = 5 * time.Minute
	outageStartupGrace   = 90 * time.Second
)

var (
	outageDown  atomic.Bool
	outageSince atomic.Int64
	outageOnce  sync.Once
	outageFails int

	outageStarting  atomic.Bool
	outageProcStart = time.Now()
	outageGraceFrom time.Time
)

func StartOutageMonitor() {
	outageOnce.Do(func() {
		logger.SetAlertSuppressor(outageSuppresses)
		go outageLoop()
	})
}

func outageLoop() {
	t := time.NewTicker(outageCheckInterval)
	defer t.Stop()
	for range t.C {
		ok, loading := outagePing()
		if !ok && !outageDown.Load() && outageInGrace(loading) {
			outageStarting.Store(true)
			outageFails = 0
			continue
		}
		outageStarting.Store(false)
		outageGraceFrom = time.Time{}
		if ok {
			outageFails = 0
			if outageDown.CompareAndSwap(true, false) {
				since := time.Unix(0, outageSince.Load())
				d := time.Since(since).Round(time.Second)
				n := logger.TakeSuppressedCount()
				logger.Info("Redis outage: recovered addr=%s downtime=%s suppressed_alerts=%d", ActiveAddr(), d, n)
				// Availability is reported once for the whole box by csuit's monitor, so no app sends a Redis state alert.
			}
			continue
		}
		outageFails++
		if outageFails >= outageFailuresToDown && outageDown.CompareAndSwap(false, true) {
			outageSince.Store(time.Now().UnixNano())
			logger.Warn("Redis outage: unreachable addr=%s, Redis availability is alerted by csuit only", ActiveAddr())
		}
	}
}

func outageInGrace(loading bool) bool {
	if loading {
		if outageGraceFrom.IsZero() {
			outageGraceFrom = time.Now()
			logger.Info("Redis is starting up (loading dataset) addr=%s, not treated as an outage unless it lasts over %s", ActiveAddr(), outageLoadingGrace)
		}
		return time.Since(outageGraceFrom) < outageLoadingGrace
	}
	return time.Since(outageProcStart) < outageStartupGrace
}

func outagePing() (bool, bool) {
	c := Client()
	if c == nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), outagePingTimeout)
	defer cancel()
	err := c.Ping(ctx).Err()
	if err == nil {
		return true, false
	}
	return false, strings.HasPrefix(strings.ToUpper(err.Error()), "LOADING")
}

func OutageActive() bool {
	return outageDown.Load()
}

func outageSuppresses(message string) bool {
	if !outageDown.Load() && !outageStarting.Load() {
		return false
	}
	m := strings.ToLower(message)
	if strings.Contains(m, "redis outage:") {
		return false
	}
	if strings.Contains(m, "redis") || strings.Contains(m, "connection refused") || strings.Contains(m, "loading ") {
		return true
	}
	if a := strings.ToLower(ActiveAddr()); a != "" && strings.Contains(m, a) {
		return true
	}
	return false
}

const outageBackoff = 15 * time.Second

func Backoff(base time.Duration) {
	if outageDown.Load() && base < outageBackoff {
		base = outageBackoff
	}
	time.Sleep(base)
}
