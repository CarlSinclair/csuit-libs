package logger

import (
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

var sentryEnabled bool

func InitSentry(service string, overrides ...string) {
	if service != "" && cfg.SentryServiceSetsName {
		serviceName = service
	}
	override := func(i int) string {
		if i < len(overrides) {
			return strings.TrimSpace(overrides[i])
		}
		return ""
	}
	dsn := override(0)
	if dsn == "" {
		dsn = strings.TrimSpace(cfg.value("SENTRY_DSN"))
	}
	if dsn == "" {
		Info("Sentry disabled (no SENTRY_DSN).")
		return
	}
	appEnv := override(1)
	if appEnv == "" {
		appEnv = "production"
	}
	if service == "" {
		service = serviceName
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:            dsn,
		Environment:    appEnv,
		Release:        service + "@" + Version,
		SendDefaultPII: false,
	})
	if err != nil {
		Error("Sentry init failed: %v", err)
		return
	}

	tenant := cfg.tenantTag()
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("app", cfg.AppName)
		scope.SetTag("service", service)
		scope.SetTag("tenant", tenant)
		scope.AddEventProcessor(func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			if event.Contexts == nil {
				event.Contexts = map[string]sentry.Context{}
			}
			event.Contexts["resources"] = resourceContext()
			return event
		})
	})

	sentryEnabled = true
	Info("Sentry initialized.")
}

func resourceContext() map[string]interface{} {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/gc/heap/goal:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples)
	mb := func(s metrics.Sample) uint64 {
		if s.Value.Kind() == metrics.KindUint64 {
			return s.Value.Uint64() >> 20
		}
		return 0
	}
	c := map[string]interface{}{
		"heap_alloc_mb": mb(samples[0]),
		"total_mem_mb":  mb(samples[1]),
		"next_gc_mb":    mb(samples[2]),
		"goroutines":    runtime.NumGoroutine(),
	}
	if samples[3].Value.Kind() == metrics.KindUint64 {
		c["gc_num"] = samples[3].Value.Uint64()
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 1 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				c["load1"] = v
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var memTotal, memAvail, swapTotal, swapFree float64
		for _, line := range strings.Split(string(b), "\n") {
			p := strings.Fields(line)
			if len(p) < 2 {
				continue
			}
			v, perr := strconv.ParseFloat(p[1], 64)
			if perr != nil {
				continue
			}
			switch strings.TrimSuffix(p[0], ":") {
			case "MemTotal":
				memTotal = v
			case "MemAvailable":
				memAvail = v
			case "SwapTotal":
				swapTotal = v
			case "SwapFree":
				swapFree = v
			}
		}
		if memTotal > 0 {
			c["mem_used_pct"] = (memTotal - memAvail) / memTotal * 100
		}
		c["swap_used_kb"] = swapTotal - swapFree
	}
	return c
}

func SentryLog(level LogLevel, formattedLog string) {
	if level >= LevelError {
		SentryCapture(level, formattedLog)
		return
	}
	SentryBreadcrumb(level, formattedLog)
}

func sentryLevel(level LogLevel) sentry.Level {
	switch level {
	case LevelDebug:
		return sentry.LevelDebug
	case LevelWarn:
		return sentry.LevelWarning
	case LevelError:
		return sentry.LevelError
	case LevelFatal:
		return sentry.LevelFatal
	default:
		return sentry.LevelInfo
	}
}

func SentryBreadcrumb(level LogLevel, formattedLog string) {
	if !sentryEnabled {
		return
	}
	sentry.AddBreadcrumb(&sentry.Breadcrumb{
		Message:   formattedLog,
		Level:     sentryLevel(level),
		Timestamp: time.Now(),
	})
}

func SentryCapture(level LogLevel, formattedLog string) {
	if !sentryEnabled {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentryLevel(level))
		sentry.CaptureMessage(formattedLog)
	})
}

func SentryRecover(v interface{}) {
	if !sentryEnabled || v == nil {
		return
	}
	hub := sentry.CurrentHub().Clone()
	hub.Recover(v)
	hub.Flush(2 * time.Second)
}

func SentryFlush() {
	if !sentryEnabled {
		return
	}
	sentry.Flush(2 * time.Second)
}
