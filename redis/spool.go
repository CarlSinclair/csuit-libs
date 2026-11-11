package redis

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type spoolEntry struct {
	Queue   string          `json:"q"`
	Payload json.RawMessage `json:"p"`
}

var (
	spoolMu   sync.Mutex
	spoolOnce sync.Once
)

const (
	spoolDirMode  os.FileMode = 0o770
	spoolFileMode os.FileMode = 0o660
)

func spoolFile() (string, error) {
	dir, err := logger.AppRootDir()
	if err != nil {
		return "", fmt.Errorf("spool directory unavailable: %w", err)
	}
	return filepath.Join(dir, "spool", "redis-enqueue.jsonl"), nil
}

func spoolAppend(queueName string, data []byte) error {
	path, err := spoolFile()
	if err != nil {
		return err
	}
	line, err := json.Marshal(spoolEntry{Queue: queueName, Payload: data})
	if err != nil {
		return err
	}
	spoolMu.Lock()
	defer spoolMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), spoolDirMode); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, spoolFileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func flushSpool() error {
	path, err := spoolFile()
	if err != nil {
		return err
	}
	spoolMu.Lock()
	defer spoolMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var lines [][]byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		if b := bytes.TrimSpace(sc.Bytes()); len(b) > 0 {
			lines = append(lines, append([]byte(nil), b...))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	pushed := 0
	var failure error
	for _, ln := range lines {
		var e spoolEntry
		if err := json.Unmarshal(ln, &e); err != nil || e.Queue == "" {
			logger.Error("Redis spool: dropping unreadable entry err=%v raw=%q", err, string(ln))
			pushed++
			continue
		}
		if err := pushOnce(e.Queue, e.Payload); err != nil {
			failure = err
			break
		}
		pushed++
	}
	if pushed == len(lines) {
		if err := os.Remove(path); err != nil {
			return err
		}
		if len(lines) > 0 {
			logger.Info("Redis spool: replayed %d spooled tasks", len(lines))
			logger.ResetPersistent("redis.spool.flush")
		}
		return nil
	}
	rest := bytes.Join(lines[pushed:], []byte("\n"))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(rest, '\n'), spoolFileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return failure
}

func startSpool() {
	spoolOnce.Do(func() {
		go func() {
			for {
				if err := flushSpool(); err != nil {
					logger.Persistent("redis.spool.flush", 5, 10*time.Minute, "Redis spool: replay failed, will retry err=%v", err)
				}
				time.Sleep(5 * time.Second)
			}
		}()
	})
}
