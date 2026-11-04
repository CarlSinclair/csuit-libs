package logger

import (
	"strings"
	"sync"
	"time"
)

const recentKept = 100

type Entry struct {
	At    time.Time
	Level string
	Msg   string
}

var (
	recentMu sync.Mutex
	recent   []Entry
)

func remember(level LogLevel, msg string) {
	if level < LevelWarn {
		return
	}
	recentMu.Lock()
	defer recentMu.Unlock()
	if len(recent) >= recentKept {
		copy(recent, recent[1:])
		recent = recent[:recentKept-1]
	}
	recent = append(recent, Entry{At: time.Now(), Level: strings.TrimSpace(level.String()), Msg: msg})
}

func Recent() []Entry {
	recentMu.Lock()
	defer recentMu.Unlock()
	out := make([]Entry, len(recent))
	for i, e := range recent {
		out[len(recent)-1-i] = e
	}
	return out
}
