package logger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type contextKey string

const (
	traceIDKey contextKey = "trace_id"
)

func NewContextWithTraceID(ctx context.Context) (context.Context, string) {
	traceID := uuid.New().String()
	return context.WithValue(ctx, traceIDKey, traceID), traceID
}

func NewContextWithTraceIDValue(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func GetTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDKey).(string); ok {
		return traceID
	}
	return ""
}

func StructuredLog(ctx context.Context, level string, component string, message string, fields map[string]interface{}) string {
	traceID := GetTraceID(ctx)

	baseMsg := fmt.Sprintf("[%s] [%s] %s", component, level, message)

	if traceID != "" {
		baseMsg = fmt.Sprintf("[trace_id=%s] %s", traceID, baseMsg)
	}

	if len(fields) > 0 {
		for k, v := range fields {
			baseMsg = fmt.Sprintf("%s %s=%v", baseMsg, k, v)
		}
	}

	return baseMsg
}

func LogWithContext(ctx context.Context, logLevel LogLevel, component, format string, v ...interface{}) {
	traceID := GetTraceID(ctx)

	if traceID != "" {
		format = fmt.Sprintf("[trace_id=%s] [%s] %s", traceID, component, format)
	} else {
		format = fmt.Sprintf("[%s] %s", component, format)
	}

	switch logLevel {
	case LevelDebug:
		globalAppLogger.Debug(format, v...)
	case LevelInfo:
		globalAppLogger.Info(format, v...)
	case LevelWarn:
		globalAppLogger.Warn(format, v...)
	case LevelError:
		globalAppLogger.Error(format, v...)
	case LevelFatal:
		globalAppLogger.Fatal(format, v...)
	}
}
