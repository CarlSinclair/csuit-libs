package tenanttime

import (
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const cacheTTL = 5 * time.Second

// Clock resolves one business timezone from a setting and converts between
// the calendar days and clock times people type or read (in that zone) and
// the UTC instants stored in the database. The setting is read through the
// source function, so a changed setting is picked up within a few seconds.
// An empty setting means UTC. A setting that names an unknown zone also falls
// back to UTC, and onError (when given) is told once per bad name.
type Clock struct {
	source  func() string
	onError func(format string, args ...interface{})

	mu      sync.Mutex
	name    string
	loc     *time.Location
	checked time.Time
	warned  map[string]bool
}

func New(source func() string, onError func(format string, args ...interface{})) *Clock {
	return &Clock{source: source, onError: onError, warned: map[string]bool{}}
}

func (c *Clock) warnOnce(key, format string, args ...interface{}) {
	if c.onError == nil || c.warned[key] {
		return
	}
	c.warned[key] = true
	c.onError(format, args...)
}

func (c *Clock) Location() *time.Location {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.loc != nil && time.Since(c.checked) < cacheTTL {
		return c.loc
	}
	name := ""
	if c.source != nil {
		name = strings.TrimSpace(c.source())
	}
	if name == "" {
		c.loc, c.name, c.checked = time.UTC, "UTC", time.Now()
		return c.loc
	}
	if c.loc != nil && c.name == name {
		c.checked = time.Now()
		return c.loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		c.warnOnce("load:"+name, "tenanttime: timezone %q could not be loaded, using UTC err=%v", name, err)
		c.loc, c.name, c.checked = time.UTC, "UTC", time.Now()
		return c.loc
	}
	c.loc, c.name, c.checked = loc, name, time.Now()
	return loc
}

// Name is the IANA name of Location, as a browser Intl API accepts it.
func (c *Clock) Name() string {
	return c.Location().String()
}

func (c *Clock) Now() time.Time {
	return time.Now().In(c.Location())
}

// Local expresses an instant in the business timezone, for display.
func (c *Clock) Local(t time.Time) time.Time {
	return t.In(c.Location())
}

// DateString is the calendar day (YYYY-MM-DD) of an instant in the business
// timezone.
func (c *Clock) DateString(t time.Time) string {
	return c.Local(t).Format("2006-01-02")
}

// DayStartUTC is the instant the business-timezone calendar day "YYYY-MM-DD"
// begins. Compare timestamps with ">= DayStartUTC(from)".
func (c *Clock) DayStartUTC(date string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date), c.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid_date %q: %w", date, err)
	}
	return t.UTC(), nil
}

// DayEndExclusiveUTC is the instant the day after "date" begins. Compare with
// "< DayEndExclusiveUTC(to)" so the whole last day is included.
func (c *Clock) DayEndExclusiveUTC(date string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(date), c.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid_date %q: %w", date, err)
	}
	return t.AddDate(0, 0, 1).UTC(), nil
}

// TodayStartUTC is the instant today began in the business timezone.
func (c *Clock) TodayStartUTC() time.Time {
	return c.DayStartOf(time.Now())
}

// DayStartOf is the instant the business-timezone day containing t began.
func (c *Clock) DayStartOf(t time.Time) time.Time {
	l := c.Local(t)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, l.Location()).UTC()
}

// MonthStartOf is the instant the business-timezone month containing t began.
func (c *Clock) MonthStartOf(t time.Time) time.Time {
	l := c.Local(t)
	return time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, l.Location()).UTC()
}

// ParseLocal turns a date ("2006-01-02") and a clock time ("15:04", seconds
// optional) typed in the business timezone into a UTC instant.
func (c *Clock) ParseLocal(date, clock string) (time.Time, error) {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	layout := "2006-01-02 15:04"
	if strings.Count(clock, ":") == 2 {
		layout = "2006-01-02 15:04:05"
	}
	t, err := time.ParseInLocation(layout, date+" "+clock, c.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid_datetime %q %q: %w", date, clock, err)
	}
	return t.UTC(), nil
}
