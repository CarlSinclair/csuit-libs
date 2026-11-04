package logger

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

var (
	ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Pattern = regexp.MustCompile(`[0-9a-fA-F:]{3,45}`)
)

const maxRememberedIPs = 4096

var (
	locationMu sync.Mutex
	locations  = map[string]string{}
)

func formatLocation(country, region, city string) string {
	country = strings.ToUpper(strings.TrimSpace(country))
	region = strings.TrimSpace(region)
	city = strings.TrimSpace(city)
	if country == "" || country == "XX" || country == "T1" {
		return ""
	}
	parts := []string{country}
	if region != "" {
		parts = append(parts, region)
	}
	if city != "" {
		parts = append(parts, city)
	}
	return "[" + strings.Join(parts, "/") + "]"
}

func RememberLocation(ip, country, region, city string) {
	loc := formatLocation(country, region, city)
	if ip == "" || loc == "" {
		return
	}
	locationMu.Lock()
	defer locationMu.Unlock()
	if len(locations) >= maxRememberedIPs {
		locations = map[string]string{}
	}
	locations[ip] = loc
}

func requestLocation(r *http.Request) string {
	return formatLocation(r.Header.Get("CF-IPCountry"), regionHeader(r), r.Header.Get("CF-IPCity"))
}

func LocationFromRequest(r *http.Request) string {
	if loc := requestLocation(r); loc != "" {
		return loc
	}
	return "[??]"
}

func RememberRequestLocation(r *http.Request, ip string) {
	RememberLocation(ip, r.Header.Get("CF-IPCountry"), regionHeader(r), r.Header.Get("CF-IPCity"))
}

func regionHeader(r *http.Request) string {
	if v := r.Header.Get("CF-Region-Code"); v != "" {
		return v
	}
	return r.Header.Get("CF-Region")
}

func locationOf(ip net.IP) string {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return ip.String()
	}
	locationMu.Lock()
	defer locationMu.Unlock()
	if l, ok := locations[ip.String()]; ok {
		return l
	}
	return "[??]"
}

func redactIPs(msg string) string {
	msg = ipv4Pattern.ReplaceAllStringFunc(msg, func(m string) string {
		if ip := net.ParseIP(m); ip != nil {
			return locationOf(ip)
		}
		return m
	})
	if !strings.Contains(msg, ":") {
		return msg
	}
	return ipv6Pattern.ReplaceAllStringFunc(msg, func(m string) string {
		if strings.Count(m, ":") >= 2 {
			if ip := net.ParseIP(m); ip != nil {
				return locationOf(ip)
			}
		}
		return m
	})
}
