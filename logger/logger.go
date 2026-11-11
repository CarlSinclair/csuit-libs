package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var defaultLogTimeZone = ""

const logFileMode = 0o640

var serviceName = "unknown"

var logTimeZone atomic.Pointer[time.Location]

func init() {
	loc := time.Local
	if defaultLogTimeZone != "" {
		var err error
		loc, err = time.LoadLocation(defaultLogTimeZone)
		if err != nil {
			log.Printf("logger: %s tzdata unavailable (%v), log timestamps will use system local time until SetTimeZone is called", defaultLogTimeZone, err)
			loc = time.Local
		}
	}
	logTimeZone.Store(loc)
}

func SetTimeZone(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("logger: SetTimeZone(%q) failed: %v - keeping previous log timezone", name, err)
		return
	}
	logTimeZone.Store(loc)
}

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

var levelNames = []string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"}

func (l LogLevel) String() string {
	if int(l) < len(levelNames) {
		return levelNames[l]
	}
	return "UNKNOWN"
}

type AppLogger struct {
	logger    *log.Logger
	minLevel  LogLevel
	formatter func(level LogLevel, file string, line int, msg string) string
	mu        sync.Mutex
}

func newAppLogger(output io.Writer, minLevelStr string, formatter func(level LogLevel, file string, line int, msg string) string) *AppLogger {
	return &AppLogger{
		logger:    log.New(output, "", 0),
		minLevel:  parseLogLevel(minLevelStr),
		formatter: formatter,
	}
}

func parseLogLevel(levelStr string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(levelStr)) {
	case "DEBUG":
		return LevelDebug
	case "INFO", "INFORMATION":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL", "CRITICAL":
		return LevelFatal
	default:
		return LevelInfo
	}
}

func createRedactingFormatter() func(level LogLevel, file string, line int, msg string) string {
	passwordHashRegex := regexp.MustCompile(`password_hash="?[a-zA-Z0-9$./]+"?`)
	tokenRegex := regexp.MustCompile(`token="?[a-zA-Z0-9_-]+"?`)
	secretRegex := regexp.MustCompile(`secret="?[a-zA-Z0-9=]+"?`)
	ipRegex := regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	filePathRegex := regexp.MustCompile(`(?i)(?:filepath|path|dir):\s*['"]?[a-z0-9_/\.-]+['"]?`)
	csrfTokenRegex := regexp.MustCompile(`name="csrf_token"([^>]*?)value="[^"]*"`)

	return func(level LogLevel, file string, line int, msg string) string {
		msg = Redact(msg)
		msg = passwordHashRegex.ReplaceAllString(msg, "[REDACTED_PASSWORD_HASH]")
		msg = tokenRegex.ReplaceAllString(msg, "token=[REDACTED_TOKEN]")
		msg = secretRegex.ReplaceAllString(msg, "secret=[REDACTED_SECRET]")
		msg = csrfTokenRegex.ReplaceAllString(msg, `name="csrf_token"${1}value="[REDACTED_CSRF_TOKEN]"`)
		msg = ipRegex.ReplaceAllString(msg, "[ANONYMIZED_IP]")
		msg = filePathRegex.ReplaceAllStringFunc(msg, func(s string) string {
			return "[REDACTED_FILE_PATH]"
		})
		remember(level, msg)
		return fmt.Sprintf("%s [%s] %s:%d %s: %s", time.Now().In(logTimeZone.Load()).Format("2006/01/02 15:04:05"), serviceName, file, line, level.String(), msg)
	}
}

func (l *AppLogger) logf(level LogLevel, format string, v ...interface{}) {
	l.logCore(level, 4, "", format, v...)
}

func (l *AppLogger) logCore(level LogLevel, skip int, alertKey string, format string, v ...interface{}) {
	if level < l.minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		file = "???"
		line = 0
	}
	shortFile := filepath.Base(file)

	msg := fmt.Sprintf(format, v...)

	if level >= LevelWarn && alertSuppressed(msg) {
		atomic.AddInt64(&outageDropped, 1)
		return
	}

	formattedLog := l.formatter(level, shortFile, line, msg)

	l.logger.Output(2, formattedLog)

	if level >= LevelError {
		enqueueTelegramAlert(formattedLog, alertKey)
	}

	if level >= LevelError {
		SentryCapture(level, formattedLog)
	} else {
		SentryBreadcrumb(level, formattedLog)
	}
}

func (l *AppLogger) Debug(format string, v ...interface{}) {
	l.logf(LevelDebug, format, v...)
}

func (l *AppLogger) Info(format string, v ...interface{}) {
	l.logf(LevelInfo, format, v...)
}

func (l *AppLogger) Warn(format string, v ...interface{}) {
	l.logf(LevelWarn, format, v...)
}

func (l *AppLogger) Error(format string, v ...interface{}) {
	l.logf(LevelError, format, v...)
}

func (l *AppLogger) ErrorKeyed(alertKey string, format string, v ...interface{}) {
	l.logCore(LevelError, 3, alertKey, format, v...)
}

func (l *AppLogger) Fatal(format string, v ...interface{}) {
	l.logf(LevelFatal, format, v...)
	SentryFlush()
	os.Exit(1)
}

var globalAppLogger *AppLogger

func init() {
	defaultOutput := os.Stdout
	defaultLevel := LevelInfo

	log.Println("logger package init: Initializing global logger with default settings.")

	globalAppLogger = newAppLogger(defaultOutput, defaultLevel.String(), createRedactingFormatter())
}

func Configure(logFilePath, logLevelStr string) error {
	var fileOutput io.Writer = nil
	var logFile *os.File
	var err error

	if logFilePath != "" {
		logFile, err = os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFileMode)
		if err != nil {
			return fmt.Errorf("failed to open log file '%s': %w", logFilePath, err)
		}
		if err := logFile.Chmod(logFileMode); err != nil {
			log.Printf("logger: could not chmod %o %q (%v); continuing", logFileMode, logFilePath, err)
		}
		fileOutput = logFile
	}

	if fileOutput != nil {
		log.SetOutput(io.MultiWriter(os.Stdout, fileOutput))
	} else {
		log.SetOutput(os.Stdout)
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	globalAppLogger.logger.SetOutput(log.Writer())
	globalAppLogger.minLevel = parseLogLevel(logLevelStr)

	Info("Logger reconfigured: level=%s output=%s", logLevelStr, logFilePath)

	return nil
}

func Debug(format string, v ...interface{}) {
	globalAppLogger.Debug(format, v...)
}
func Info(format string, v ...interface{}) {
	globalAppLogger.Info(format, v...)
}
func Warn(format string, v ...interface{}) {
	globalAppLogger.Warn(format, v...)
}
func Error(format string, v ...interface{}) {
	globalAppLogger.Error(format, v...)
}
func Fatal(format string, v ...interface{}) {
	globalAppLogger.Fatal(format, v...)
}

func ErrorKeyed(alertKey string, format string, v ...interface{}) {
	globalAppLogger.ErrorKeyed(alertKey, format, v...)
}

func NewError(format string, v ...interface{}) error {
	return fmt.Errorf(format, v...)
}
