package redis

import (
	"context"
	"fmt"
	"os"
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
)

var (
	outageDown  atomic.Bool
	outageSince atomic.Int64
	outageOnce  sync.Once
	outageFails int
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
		if outagePing() {
			outageFails = 0
			if outageDown.CompareAndSwap(true, false) {
				since := time.Unix(0, outageSince.Load())
				d := time.Since(since).Round(time.Second)
				n := logger.TakeSuppressedCount()
				logger.Info("Redis outage: recovered addr=%s downtime=%s suppressed_alerts=%d", ActiveAddr(), d, n)
				logger.SendStateAlert("redis-outage", fmt.Sprintf("Redis is back up (%s, down for %s). %d Redis-related alert(s) were suppressed during the outage; every one is in the journal.", hostLabel(), d, n))
			}
			continue
		}
		outageFails++
		if outageFails >= outageFailuresToDown && outageDown.CompareAndSwap(false, true) {
			outageSince.Store(time.Now().UnixNano())
			logger.Error("Redis outage: unreachable addr=%s, further Redis errors are not alerted until it is back", ActiveAddr())
			logger.SendStateAlert("redis-outage", "Redis is DOWN ("+hostLabel()+"). Further Redis errors are silenced until it recovers.")
		}
	}
}

func outagePing() bool {
	c := Client()
	if c == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), outagePingTimeout)
	defer cancel()
	return c.Ping(ctx).Err() == nil
}

func OutageActive() bool {
	return outageDown.Load()
}

func outageSuppresses(message string) bool {
	if !outageDown.Load() {
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

func hostLabel() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown process"
	}
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		exe = exe[i+1:]
	}
	return exe
}

const outageBackoff = 15 * time.Second

func Backoff(base time.Duration) {
	if outageDown.Load() && base < outageBackoff {
		base = outageBackoff
	}
	time.Sleep(base)
}
