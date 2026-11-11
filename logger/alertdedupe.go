package logger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var alertBackoff = []time.Duration{45 * time.Second, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const (
	alertQuietReset = 10 * time.Minute
	alertMaxKeys    = 2000
	alertSampleMax  = 400
	alertStateFile  = "alert-dedupe.json"
)

type alertEntry struct {
	Level       int       `json:"level"`
	NextAllowed time.Time `json:"next_allowed"`
	LastSeen    time.Time `json:"last_seen"`
	Suppressed  int       `json:"suppressed"`
	Sample      string    `json:"sample"`
	Exact       bool      `json:"exact,omitempty"`
}

var (
	alertMu     sync.Mutex
	alertSeen   = map[string]*alertEntry{}
	alertLoaded bool
	alertDirty  bool
)

func alertStatePath() (string, error) {
	dir, err := AppRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spool", alertStateFile), nil
}

func alertLoadLocked() {
	if alertLoaded {
		return
	}
	alertLoaded = true
	path, err := alertStatePath()
	if err != nil {
		log.Printf("ALERT DEDUPE: state path unavailable, dedupe is memory-only: %v", err)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("ALERT DEDUPE: reading %s failed, starting empty: %v", path, err)
		}
		return
	}
	var loaded map[string]*alertEntry
	if err := json.Unmarshal(raw, &loaded); err != nil {
		log.Printf("ALERT DEDUPE: %s is corrupt, starting empty: %v", path, err)
		return
	}
	for k, e := range loaded {
		if e != nil {
			alertSeen[k] = e
		}
	}
}

func alertSaveLocked() {
	if !alertDirty {
		return
	}
	path, err := alertStatePath()
	if err != nil {
		return
	}
	raw, err := json.Marshal(alertSeen)
	if err != nil {
		log.Printf("ALERT DEDUPE: encoding state failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o775); err != nil {
		log.Printf("ALERT DEDUPE: creating %s failed, state not persisted: %v", filepath.Dir(path), err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		log.Printf("ALERT DEDUPE: writing %s failed, state not persisted: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("ALERT DEDUPE: renaming %s failed, state not persisted: %v", tmp, err)
		return
	}
	alertDirty = false
}

func alertExactKey(message string) string {
	sum := sha256.Sum256([]byte("exact:" + message))
	return hex.EncodeToString(sum[:])
}

func alertCollapsedKey(message, alertKey string) string {
	if alertKey != "" {
		sum := sha256.Sum256([]byte("key:" + alertKey))
		return hex.EncodeToString(sum[:])
	}
	sum := telegramDedupKey(message)
	return hex.EncodeToString(sum[:])
}

func alertResetAfter(e *alertEntry) time.Duration {
	d := 2 * alertBackoff[e.Level]
	if d < alertQuietReset {
		d = alertQuietReset
	}
	return d
}

func alertGate(message, key string, exact bool) (string, bool) {
	now := time.Now()
	alertMu.Lock()
	defer alertMu.Unlock()
	alertLoadLocked()

	e := alertSeen[key]
	if e != nil && now.Before(e.NextAllowed) {
		e.Suppressed++
		e.LastSeen = now
		alertDirty = true
		return "", false
	}
	level := 0
	suppressed := 0
	if e != nil && now.Sub(e.LastSeen) <= alertResetAfter(e) {
		suppressed = e.Suppressed
		if !exact && e.Level+1 < len(alertBackoff) {
			level = e.Level + 1
		} else if !exact {
			level = len(alertBackoff) - 1
		}
	}
	if e == nil && len(alertSeen) >= alertMaxKeys {
		alertPruneLocked(now)
		if len(alertSeen) >= alertMaxKeys {
			alertEvictOldestLocked()
		}
	}
	sample := message
	if len(sample) > alertSampleMax {
		sample = sample[:alertSampleMax]
	}
	alertSeen[key] = &alertEntry{Level: level, NextAllowed: now.Add(alertBackoff[level]), LastSeen: now, Sample: sample, Exact: exact}
	alertDirty = true
	if suppressed > 0 {
		message += fmt.Sprintf("\n\n(%d similar alert(s) suppressed since the last one; next alert for this issue in at least %s)", suppressed, alertBackoff[level])
	}
	return message, true
}

func alertEvictOldestLocked() {
	var oldest string
	var oldestAt time.Time
	for k, e := range alertSeen {
		if oldest == "" || e.LastSeen.Before(oldestAt) {
			oldest, oldestAt = k, e.LastSeen
		}
	}
	if oldest != "" {
		delete(alertSeen, oldest)
		alertDirty = true
	}
}

func alertPruneLocked(now time.Time) []string {
	var summaries []string
	for k, e := range alertSeen {
		if now.Sub(e.LastSeen) <= alertResetAfter(e) {
			continue
		}
		if e.Suppressed > 0 && !e.Exact {
			summaries = append(summaries, fmt.Sprintf("%s\n\n(issue went quiet; %d further occurrence(s) were suppressed)", e.Sample, e.Suppressed))
		}
		delete(alertSeen, k)
		alertDirty = true
	}
	return summaries
}

func alertSweep() []string {
	alertMu.Lock()
	defer alertMu.Unlock()
	alertLoadLocked()
	summaries := alertPruneLocked(time.Now())
	alertSaveLocked()
	return summaries
}

func SendOperatorMessage(message, parseMode string) bool {
	chatID := GetTelegramChatID()
	if GetTelegramBotToken() == "" || chatID == "" {
		return false
	}
	gated, ok := alertGate(message, alertExactKey(message), true)
	if !ok {
		return true
	}
	text := gated
	if parseMode == "MarkdownV2" {
		text = formatTelegramAlert(gated)
	}
	sent, detail := postTelegramRetry(chatID, text, parseMode, 10*time.Second)
	if !sent {
		log.Printf("TELEGRAM ERROR: operator message failed: %s", detail)
	}
	return sent
}
