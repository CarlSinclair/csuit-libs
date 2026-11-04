package logger

import "time"

type RetryPolicy struct {
	Key       string
	Attempts  int
	Backoff   []time.Duration
	Threshold int
	Window    time.Duration
	Permanent func(error) bool
}

func ResetPersistent(key string) {
	persistMu.Lock()
	delete(persistSeen, key)
	persistMu.Unlock()
}

func Retry(p RetryPolicy, what string, op func() error) error {
	if p.Attempts < 1 {
		p.Attempts = 1
	}
	if p.Threshold < 1 {
		p.Threshold = 3
	}
	if p.Window <= 0 {
		p.Window = 10 * time.Minute
	}
	if p.Key == "" {
		p.Key = what
	}
	var err error
	for attempt := 1; attempt <= p.Attempts; attempt++ {
		err = op()
		if err == nil {
			ResetPersistent(p.Key)
			return nil
		}
		if p.Permanent != nil && p.Permanent(err) {
			Error("%s failed permanently: %v", what, err)
			return err
		}
		if attempt < p.Attempts {
			wait := time.Duration(0)
			if len(p.Backoff) > 0 {
				idx := attempt - 1
				if idx >= len(p.Backoff) {
					idx = len(p.Backoff) - 1
				}
				wait = p.Backoff[idx]
			}
			Warn("%s attempt %d/%d failed, retrying in %s: %v", what, attempt, p.Attempts, wait, err)
			time.Sleep(wait)
		}
	}
	Persistent(p.Key, p.Threshold, p.Window, "%s gave up after %d attempts: %v", what, p.Attempts, err)
	return err
}
