package timezones

import (
	"fmt"
	"sort"
	"strings"
)

// SupportedTimezone is one row of the Timezone dropdown on Business
// Settings (and the controller's own Global Settings). Name is the IANA
// zone; StdOffsetMinutes is that zone's standard (non-DST) offset from UTC
// in minutes, stored statically here rather than resolved through
// time.LoadLocation at runtime - so the option list AND the "(UTC-03:00)"
// aid shown beside each name stay byte-identical on every box regardless
// of what tzdata happens to be installed. DST is true for the zones whose
// clocks still shift twice a year, so callers can note that the printed
// offset is the standard-time one.
//
// One entry per distinct current standard offset + DST behavior, not one
// per city or country - e.g. Brazil alone spans 4 distinct offsets
// (Noronha -2, Sao_Paulo -3, Manaus -4, Rio_Branco -5) and gets exactly
// one representative at each rather than the dozen same-offset Brazilian
// city zones IANA carries for pre-2019 historical reasons (Brazil
// abolished DST nationally in 2019). Two zones sharing a base offset both
// stay in the list only when their DST rule genuinely differs (e.g.
// America/Denver observes DST, America/Phoenix never does, despite both
// being -7). The literal below is kept alphabetical so it stays easy to
// audit and dedupe; the dropdown itself renders west-to-east by UTC
// offset - see GetSupportedTimezoneOptions.
type SupportedTimezone struct {
	Name             string
	StdOffsetMinutes int
	DST              bool
}

var SupportedTimezones = []SupportedTimezone{
	{"Africa/Johannesburg", 120, false},
	{"America/Anchorage", -540, true},
	{"America/Bogota", -300, false},
	{"America/Chicago", -360, true},
	{"America/Denver", -420, true},
	{"America/Halifax", -240, true},
	{"America/Los_Angeles", -480, true},
	{"America/Manaus", -240, false},
	{"America/Mexico_City", -360, false},
	{"America/New_York", -300, true},
	{"America/Noronha", -120, false},
	{"America/Phoenix", -420, false},
	{"America/Santiago", -240, true},
	{"America/Sao_Paulo", -180, false},
	{"America/St_Johns", -210, true},
	{"Asia/Bangkok", 420, false},
	{"Asia/Dhaka", 360, false},
	{"Asia/Dubai", 240, false},
	{"Asia/Kabul", 270, false},
	{"Asia/Karachi", 300, false},
	{"Asia/Kathmandu", 345, false},
	{"Asia/Kolkata", 330, false},
	{"Asia/Shanghai", 480, false},
	{"Asia/Tehran", 210, false},
	{"Asia/Tokyo", 540, false},
	{"Asia/Yangon", 390, false},
	{"Atlantic/Azores", -60, true},
	{"Australia/Adelaide", 570, true},
	{"Australia/Brisbane", 600, false},
	{"Australia/Sydney", 600, true},
	{"Europe/London", 0, true},
	{"Europe/Moscow", 180, false},
	{"Europe/Paris", 60, true},
	{"Pacific/Auckland", 720, true},
	{"Pacific/Guadalcanal", 660, false},
	{"Pacific/Honolulu", -600, false},
	{"Pacific/Kiritimati", 840, false},
	{"Pacific/Marquesas", -570, false},
	{"Pacific/Pago_Pago", -660, false},
	{"Pacific/Tongatapu", 780, false},
	{"UTC", 0, false},
}

// OffsetLabel renders the standard offset as "UTC", "UTC+05:30",
// "UTC-03:00" - the visual aid that lets someone pick the right zone
// without knowing which IANA city name maps to their clock.
func (t SupportedTimezone) OffsetLabel() string {
	if t.StdOffsetMinutes == 0 {
		return "UTC"
	}
	sign := "+"
	m := t.StdOffsetMinutes
	if m < 0 {
		sign = "-"
		m = -m
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, m/60, m%60)
}

// DisplayLabel is what the dropdown shows: "(UTC-03:00) America/Sao_Paulo".
func (t SupportedTimezone) DisplayLabel() string {
	return fmt.Sprintf("(%s) %s", t.OffsetLabel(), t.Name)
}

// TimezoneOption is a value/label pair for rendering an <option> without
// the template needing to call methods on the struct.
type TimezoneOption struct {
	Value string
	Label string
}

// GetSupportedTimezones returns just the IANA names, in list order - a
// copy, so a caller can't mutate shared state. Kept for callers that only
// need the identifier (e.g. validation).
func GetSupportedTimezones() []string {
	out := make([]string, len(SupportedTimezones))
	for i, t := range SupportedTimezones {
		out[i] = t.Name
	}
	return out
}

// GetSupportedTimezoneOptions returns the value/label pairs backing the
// dropdown, each label carrying its UTC offset aid, ordered west-to-east
// by standard offset (UTC-11:00 first, UTC+14:00 last) with same-offset
// zones falling back to alphabetical - so someone picks by the "(UTC-03:00)"
// aid rather than hunting an alphabetical wall of IANA city names.
func GetSupportedTimezoneOptions() []TimezoneOption {
	sorted := make([]SupportedTimezone, len(SupportedTimezones))
	copy(sorted, SupportedTimezones)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StdOffsetMinutes != sorted[j].StdOffsetMinutes {
			return sorted[i].StdOffsetMinutes < sorted[j].StdOffsetMinutes
		}
		return sorted[i].Name < sorted[j].Name
	})
	out := make([]TimezoneOption, len(sorted))
	for i, t := range sorted {
		out[i] = TimezoneOption{Value: t.Name, Label: t.DisplayLabel()}
	}
	return out
}

// IsSupportedTimezone reports whether name is one of the dropdown's IANA
// zones - a deterministic list-membership check, deliberately not a
// time.LoadLocation call, so a submitted value is accepted or rejected the
// same way on every box regardless of installed tzdata.
func IsSupportedTimezone(name string) bool {
	name = strings.TrimSpace(name)
	for _, t := range SupportedTimezones {
		if t.Name == name {
			return true
		}
	}
	return false
}
