package currency

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"

	xcurrency "golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

var ErrUnavailable = errors.New("live exchange rate unavailable")

var Fallback = []string{"USD", "BRL", "EUR", "GBP", "CAD"}

var (
	FreshFor    = 12 * time.Hour
	MaxStale    = 72 * time.Hour
	RetryAfter  = 5 * time.Minute
	BaseCurrent = func() string { return "USD" }
	Fetch       = FetchERAPI
)

var (
	cacheMu sync.RWMutex
	cache   = map[string]snapshot{}
	retryAt = map[string]time.Time{}
)

type snapshot struct {
	Base      string
	Rates     map[string]float64
	UpdatedAt time.Time
}

func Default() string { return "USD" }

func Normalize(raw string) string {
	curr := strings.ToUpper(strings.TrimSpace(raw))
	if len(curr) != 3 {
		return ""
	}
	unit, err := xcurrency.ParseISO(curr)
	if err != nil {
		return ""
	}
	if scale, _ := xcurrency.Standard.Rounding(unit); scale != 2 {
		return ""
	}
	return curr
}

func Known() []string {
	cacheMu.RLock()
	seen := map[string]bool{}
	for _, snap := range cache {
		for curr := range snap.Rates {
			if Normalize(curr) == curr {
				seen[curr] = true
			}
		}
	}
	cacheMu.RUnlock()
	out := make([]string, 0, len(seen))
	for curr := range seen {
		out = append(out, curr)
	}
	sort.Strings(out)
	return out
}

func Supported() []string {
	if known := Known(); len(known) > 0 {
		return known
	}
	out := make([]string, len(Fallback))
	copy(out, Fallback)
	sort.Strings(out)
	return out
}

func Reset() {
	cacheMu.Lock()
	cache = map[string]snapshot{}
	retryAt = map[string]time.Time{}
	cacheMu.Unlock()
}

func Refresh() error { return RefreshFor(BaseCurrent()) }

func RefreshFor(raw string) error {
	base := Normalize(raw)
	if base == "" {
		base = Default()
	}
	if _, err := refreshWithRetry(base); err != nil {
		reportFailure("Currency Sync", base, err, true)
		return fmt.Errorf("currency rate refresh base=%s: %w", base, err)
	}
	logger.Info("Live Currency Rates Updated. base=%s", base)
	return nil
}

func Rate(from, to string) (float64, bool) {
	from, to = pair(from, to)
	if from == to {
		return 1.0, true
	}
	snap, ok, err := ensure(from)
	if err != nil {
		logger.Error("Currency rate lookup failed: from=%s to=%s err=%v", from, to, err)
	}
	if !ok {
		return 0, false
	}
	rate, exists := snap.Rates[to]
	if !exists || rate <= 0 {
		logger.Error("Currency rate missing: from=%s to=%s", from, to)
		return 0, false
	}
	return rate, true
}

func StrictRate(from, to string) (float64, error) {
	from, to = pair(from, to)
	if from == to {
		return 1.0, nil
	}
	snap, ok, err := ensure(from)
	if !ok {
		return 0, fmt.Errorf("%w: %s -> %s: %v", ErrUnavailable, from, to, err)
	}
	if age := time.Since(snap.UpdatedAt); age > MaxStale {
		return 0, fmt.Errorf("%w: %s -> %s: rates are %s old: %v", ErrUnavailable, from, to, age.Round(time.Minute), err)
	}
	rate, exists := snap.Rates[to]
	if !exists || rate <= 0 {
		return 0, fmt.Errorf("%w: no rate for %s", ErrUnavailable, to)
	}
	return rate, nil
}

func Convert(cents int64, from, to string) (int64, float64, bool) {
	from, to = pair(from, to)
	if from == to {
		return cents, 1.0, true
	}
	rate, ok := Rate(from, to)
	if !ok {
		return cents, 0, false
	}
	return int64(math.Round(float64(cents) * rate)), rate, true
}

func ConvertAndFormat(cents int64, from, to string) string {
	from, to = pair(from, to)
	converted := cents
	if from != to {
		if out, _, ok := Convert(cents, from, to); ok {
			converted = CleanStep(out, to)
		} else {
			logger.Error("Currency conversion fallback to base amount: from=%s to=%s cents=%d", from, to, cents)
			to = from
		}
	}
	return Format(converted, to)
}

func pair(from, to string) (string, string) {
	from = Normalize(from)
	to = Normalize(to)
	if from == "" {
		from = Default()
	}
	if to == "" {
		to = from
	}
	return from, to
}

func CleanStep(cents int64, cur string) int64 {
	if cents <= 0 {
		return cents
	}
	cur = strings.ToUpper(strings.TrimSpace(cur))
	switch {
	case cents < 100:
		stepped := int64(math.Round(float64(cents)/5.0)) * 5
		if stepped < 5 {
			stepped = 5
		}
		return stepped
	case cents >= 100000:
		return int64(math.Round(float64(cents)/100.0)) * 100
	case cur == "BRL":
		return int64(math.Round(float64(cents-90)/100.0))*100 + 90
	default:
		return int64(math.Round(float64(cents+1)/100.0))*100 - 1
	}
}

func Format(cents int64, code string) string {
	code = Normalize(code)
	if code == "" {
		code = Default()
	}
	value := float64(cents) / 100.0
	loc := language.AmericanEnglish
	prefix := code + " "
	switch code {
	case "BRL":
		loc, prefix = language.BrazilianPortuguese, "R$ "
	case "EUR":
		loc, prefix = language.German, "€ "
	case "GBP":
		loc, prefix = language.BritishEnglish, "£ "
	case "CAD":
		loc, prefix = language.MustParse("en-CA"), "C$ "
	case "ARS":
		loc, prefix = language.Spanish, "AR$ "
	case "USD":
		prefix = "US$ "
	}
	return message.NewPrinter(loc).Sprintf("%s%.2f", prefix, value)
}

func ensure(base string) (snapshot, bool, error) {
	base = Normalize(base)
	if base == "" {
		base = Default()
	}
	cacheMu.RLock()
	snap, ok := cache[base]
	cacheMu.RUnlock()
	if !ok {
		if derived, found := derive(base); found {
			snap, ok = derived, true
		}
	}
	if !ok {
		if persisted, found := loadPersisted(base); found {
			cacheMu.Lock()
			if cur, exists := cache[base]; exists {
				persisted = cur
			} else {
				cache[base] = persisted
			}
			cacheMu.Unlock()
			snap, ok = persisted, true
		}
	}
	if ok && time.Since(snap.UpdatedAt) < FreshFor && len(snap.Rates) > 0 {
		return snap, true, nil
	}
	if ok && len(snap.Rates) > 0 {
		cacheMu.RLock()
		wait := retryAt[base]
		cacheMu.RUnlock()
		if time.Now().Before(wait) {
			return snap, true, nil
		}
	}
	fresh, err := refresh(base)
	if err != nil {
		if ok && len(snap.Rates) > 0 {
			cacheMu.Lock()
			retryAt[base] = time.Now().Add(RetryAfter)
			cacheMu.Unlock()
			reportFailure("Currency refresh", base, err, false)
			return snap, true, err
		}
		return snapshot{}, false, err
	}
	return fresh, true, nil
}

func refresh(base string) (snapshot, error) {
	base = Normalize(base)
	if base == "" {
		base = Default()
	}
	table, err := Fetch(base)
	if err != nil {
		return snapshot{}, err
	}
	next := snapshot{Base: base, Rates: map[string]float64{base: 1.0}, UpdatedAt: time.Now().UTC()}
	for curr, rate := range table {
		if rate > 0 && Normalize(curr) == curr && curr != base {
			next.Rates[curr] = rate
		}
	}
	if len(next.Rates) < 2 {
		return snapshot{}, fmt.Errorf("provider returned no usable rates for base %s", base)
	}
	cacheMu.Lock()
	cache[base] = next
	delete(retryAt, base)
	cacheMu.Unlock()
	persist(next)
	return next, nil
}

func derive(base string) (snapshot, bool) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	var best snapshot
	for _, snap := range cache {
		if snap.Rates[base] <= 0 {
			continue
		}
		if best.Rates == nil || snap.UpdatedAt.After(best.UpdatedAt) {
			best = snap
		}
	}
	if best.Rates == nil {
		return snapshot{}, false
	}
	pivot := best.Rates[base]
	out := snapshot{Base: base, Rates: make(map[string]float64, len(best.Rates)), UpdatedAt: best.UpdatedAt}
	for curr, rate := range best.Rates {
		if rate > 0 {
			out.Rates[curr] = rate / pivot
		}
	}
	out.Rates[base] = 1.0
	return out, true
}
