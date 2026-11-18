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

type AlertStateStore interface {
	Load() ([]byte, error)
	Save([]byte) error
}

var alertStore AlertStateStore

func SetAlertStateStore(s AlertStateStore) {
	alertMu.Lock()
	alertStore = s
	alertLoaded = false
	alertMu.Unlock()
}

type fileAlertStore struct{ path string }

func FileAlertStore(dir string) AlertStateStore {
	return fileAlertStore{path: filepath.Join(dir, alertStateFile)}
}

func (f fileAlertStore) Load() ([]byte, error) {
	raw, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.path, err)
	}
	return raw, nil
}

func (f fileAlertStore) Save(raw []byte) error {
	if err := EnsureDir(filepath.Dir(f.path), 0o770); err != nil {
		if _, serr := os.Stat(filepath.Dir(f.path)); serr != nil {
			return err
		}
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o660); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("renaming %s: %w", tmp, err)
	}
	return nil
}

func currentAlertStore() (AlertStateStore, error) {
	if alertStore != nil {
		return alertStore, nil
	}
	dir, err := AppRootDir()
	if err != nil {
		return nil, err
	}
	return FileAlertStore(filepath.Join(dir, "spool")), nil
}

func alertLoadLocked() {
	if alertLoaded {
		return
	}
	alertLoaded = true
	st, err := currentAlertStore()
	if err != nil {
		log.Printf("ALERT DEDUPE: state store unavailable, dedupe is memory-only: %v", err)
		return
	}
	raw, err := st.Load()
	if err != nil {
		log.Printf("ALERT DEDUPE: loading state failed, starting empty: %v", err)
		return
	}
	if len(raw) == 0 {
		return
	}
	var loaded map[string]*alertEntry
	if err := json.Unmarshal(raw, &loaded); err != nil {
		log.Printf("ALERT DEDUPE: stored state is corrupt, starting empty: %v", err)
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
	st, err := currentAlertStore()
	if err != nil {
		log.Printf("ALERT DEDUPE: state store unavailable, state not persisted: %v", err)
		return
	}
	raw, err := json.Marshal(alertSeen)
	if err != nil {
		log.Printf("ALERT DEDUPE: encoding state failed: %v", err)
		return
	}
	if err := st.Save(raw); err != nil {
		log.Printf("ALERT DEDUPE: state not persisted: %v", err)
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
