package secheaders

import "testing"

func TestValidateAccepts(t *testing.T) {
	e := &Editor{}
	f := Form{XFrameOptions: "DENY", HSTSMaxAge: "63072000", HSTSIncludeSubs: true, HSTSPreload: true,
		ContentTypeOptions: "nosniff", ReferrerPolicy: "no-referrer",
		PermissionsPolicy: "camera=(self)\nmicrophone=()\ngeolocation=(self \"https://a.example\")",
		CSPExtra:          "img-src https://cdn.example.com data:\nconnect-src https://*.example.com:443",
		CORSOrigins:       "https://a.example.com\nhttp://localhost:3000"}
	vals, errs := e.Validate(f)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	if vals[KeyPermissionsPolicy] != "camera=(self), microphone=(), geolocation=(self \"https://a.example\")" {
		t.Fatalf("permissions %q", vals[KeyPermissionsPolicy])
	}
	if vals[KeyCORSOrigins] != "https://a.example.com,http://localhost:3000" {
		t.Fatalf("cors %q", vals[KeyCORSOrigins])
	}
}

func TestValidateRejects(t *testing.T) {
	e := &Editor{}
	cases := map[string]Form{
		"xframe":       {XFrameOptions: "ALLOW-FROM x"},
		"hsts_max_age": {HSTSMaxAge: "-1"},
		"hsts_preload": {HSTSMaxAge: "100", HSTSPreload: true, HSTSIncludeSubs: true},
		"nosniff":      {ContentTypeOptions: "sniff"},
		"referrer":     {ReferrerPolicy: "bogus"},
		"permissions":  {PermissionsPolicy: "camera=self"},
		"csp_extra":    {CSPExtra: "img-src https://a.com;script-src *"},
		"cors":         {CORSOrigins: "*"},
	}
	for field, f := range cases {
		if _, errs := e.Validate(f); errs[field] == "" {
			t.Errorf("%s not rejected: %v", field, errs)
		}
	}
	if _, errs := e.Validate(Form{CSPExtra: "script-src 'unsafe-inline'"}); errs["csp_extra"] != ErrCSPUnsafe {
		t.Errorf("unsafe-inline in script-src not rejected: %v", errs)
	}
	if _, errs := e.Validate(Form{CSPExtra: "script-src https://a.com\nbogus-src x"}); errs["csp_extra"] == "" {
		t.Errorf("unknown directive not rejected")
	}
	if _, errs := e.Validate(Form{CSPFull: "default-src 'self'; frame-ancestors 'none'"}); len(errs) != 0 {
		t.Errorf("valid full csp rejected: %v", errs)
	}
}
