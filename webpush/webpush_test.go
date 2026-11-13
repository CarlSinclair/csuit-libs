package webpush

import "testing"

func TestEndpointValidation(t *testing.T) {
	cases := []struct {
		raw     string
		browser bool
		device  bool
	}{
		{"https://fcm.googleapis.com/fcm/send/abc", true, true},
		{"https://updates.push.services.mozilla.com/wpush/v2/abc", true, true},
		{"https://ntfy.example.org/upabc?up=1", false, true},
		{"https://ntfy.example.org:8443/upabc", false, false},
		{"http://ntfy.example.org/upabc", false, false},
		{"https://127.0.0.1/upabc", false, false},
		{"https://localhost/upabc", false, false},
		{"https://user:pw@ntfy.example.org/upabc", false, false},
	}
	for _, c := range cases {
		if err := ValidatePushEndpoint(c.raw); (err == nil) != c.browser {
			t.Errorf("browser %s: err=%v, want ok=%v", c.raw, err, c.browser)
		}
		if err := ValidateDeviceEndpoint(c.raw); (err == nil) != c.device {
			t.Errorf("device %s: err=%v, want ok=%v", c.raw, err, c.device)
		}
	}
}
