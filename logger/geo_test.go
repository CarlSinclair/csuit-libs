package logger

import "testing"

func TestRedactIPsToLocation(t *testing.T) {
	RememberLocation("203.0.113.9", "nl", "NH", "Amsterdam")
	RememberLocation("2001:db8::7", "BR", "", "")
	cases := map[string]string{
		"login failed from 203.0.113.9 now": "login failed from [NL/NH/Amsterdam] now",
		"peer 2001:db8:0:0:0:0:0:7 bad":     "peer [BR] bad",
		"unknown 198.51.100.4 here":         "unknown [??] here",
		"redis 127.0.0.1:9736 up":           "redis 127.0.0.1:9736 up",
		"fires at 12:30:45 for show 7":      "fires at 12:30:45 for show 7",
	}
	for in, want := range cases {
		if got := redactIPs(in); got != want {
			t.Errorf("redactIPs(%q) = %q, want %q", in, got, want)
		}
	}
}
