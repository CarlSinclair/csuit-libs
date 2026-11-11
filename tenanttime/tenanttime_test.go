package tenanttime

import (
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	zone := "America/Sao_Paulo"
	var warned int
	c := New(func() string { return zone }, func(string, ...interface{}) { warned++ })

	start, err := c.DayStartUTC("2026-11-05")
	if err != nil || !start.Equal(time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("DayStartUTC = %v %v", start, err)
	}
	end, _ := c.DayEndExclusiveUTC("2026-11-05")
	if !end.Equal(time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("DayEndExclusiveUTC = %v", end)
	}
	at, err := c.ParseLocal("2026-11-05", "21:30")
	if err != nil || !at.Equal(time.Date(2026, 11, 6, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("ParseLocal = %v %v", at, err)
	}
	if got := c.DateString(at); got != "2026-11-05" {
		t.Fatalf("DateString = %s", got)
	}
	if c.Name() != zone {
		t.Fatalf("Name = %s", c.Name())
	}
	if _, err := c.ParseLocal("x", "y"); err == nil {
		t.Fatal("ParseLocal accepted junk")
	}

	empty := New(func() string { return "" }, nil)
	if empty.Location() != time.UTC {
		t.Fatal("empty setting is not UTC")
	}
	bad := New(func() string { return "Nope/Zone" }, func(string, ...interface{}) { warned++ })
	bad.Location()
	bad.Location()
	if bad.Location() != time.UTC || warned != 1 {
		t.Fatalf("bad zone: warned=%d", warned)
	}
}
