package secheaders

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	ErrXFrame      = "sec_hdr_err_xframe"
	ErrHSTSMaxAge  = "sec_hdr_err_hsts_max_age"
	ErrHSTSPreload = "sec_hdr_err_hsts_preload"
	ErrNoSniff     = "sec_hdr_err_nosniff"
	ErrReferrer    = "sec_hdr_err_referrer"
	ErrPermissions = "sec_hdr_err_permissions"
	ErrCSPExtra    = "sec_hdr_err_csp_extra"
	ErrCSPUnsafe   = "sec_hdr_err_csp_unsafe"
	ErrCSPFull     = "sec_hdr_err_csp_full"
	ErrCORS        = "sec_hdr_err_cors"
)

var (
	XFrameChoices   = []string{"DENY", "SAMEORIGIN"}
	ReferrerChoices = []string{"no-referrer", "no-referrer-when-downgrade", "origin", "origin-when-cross-origin", "same-origin", "strict-origin", "strict-origin-when-cross-origin", "unsafe-url"}

	cspDirectives = map[string]bool{
		"default-src": true, "script-src": true, "script-src-elem": true, "script-src-attr": true,
		"style-src": true, "style-src-elem": true, "style-src-attr": true, "img-src": true,
		"font-src": true, "connect-src": true, "media-src": true, "object-src": true,
		"frame-src": true, "child-src": true, "worker-src": true, "manifest-src": true,
		"base-uri": true, "form-action": true, "frame-ancestors": true, "prefetch-src": true,
		"upgrade-insecure-requests": true, "block-all-mixed-content": true,
	}
	scriptDirectives = map[string]bool{"script-src": true, "script-src-elem": true, "script-src-attr": true, "default-src": true}

	cspKeyword  = regexp.MustCompile(`^'(self|none|strict-dynamic|unsafe-inline|unsafe-eval|unsafe-hashes|wasm-unsafe-eval|report-sample)'$`)
	cspNonce    = regexp.MustCompile(`^'(nonce-[A-Za-z0-9+/=_-]+|sha(256|384|512)-[A-Za-z0-9+/=_-]+)'$`)
	cspScheme   = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:$`)
	cspHost     = regexp.MustCompile(`^(https?://|wss?://)?(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:([0-9]{1,5}|\*))?(/[A-Za-z0-9._~!$&()*+=:@%/-]*)?$`)
	featureName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	featureList = regexp.MustCompile(`^(\*|\(\)|\((self|\*|"https?://[A-Za-z0-9.*:-]+")( (self|\*|"https?://[A-Za-z0-9.*:-]+"))*\))$`)
)

type Form struct {
	XFrameOptions      string
	HSTSDisabled       bool
	HSTSMaxAge         string
	HSTSIncludeSubs    bool
	HSTSPreload        bool
	ContentTypeOptions string
	ReferrerPolicy     string
	PermissionsPolicy  string
	CSPExtra           string
	CSPFull            string
	CORSOrigins        string
}

type View struct {
	Form      Form
	Errors    map[string]string
	Effective EffectiveHeaders
	Defaults  EffectiveHeaders
}

type EffectiveHeaders struct {
	XFrameOptions     string
	HSTS              string
	ContentTypeOpts   string
	ReferrerPolicy    string
	PermissionsPolicy string
	CSP               string
	CORSOrigins       string
}

type Change struct {
	Key string
	Old string
	New string
}

type Editor struct {
	Get    Settings
	Set    func(key, value string) error
	Live   *Live
	Reload func()
}

func (e *Editor) stored(key string) string {
	if e.Get == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(key))
}

func linesFromCSV(s string) string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

func (e *Editor) Load() View {
	f := Form{
		XFrameOptions:      e.stored(KeyXFrameOptions),
		HSTSMaxAge:         e.stored(KeyHSTSMaxAge),
		HSTSDisabled:       strings.EqualFold(e.stored(KeyHSTSDisabled), "true"),
		HSTSIncludeSubs:    e.storedBoolDefault(KeyHSTSIncludeSubs, true),
		HSTSPreload:        e.storedBoolDefault(KeyHSTSPreload, true),
		ContentTypeOptions: e.stored(KeyXContentTypeOptions),
		ReferrerPolicy:     e.stored(KeyReferrerPolicy),
		PermissionsPolicy:  strings.Join(splitPermissions(e.stored(KeyPermissionsPolicy)), "\n"),
		CSPFull:            e.stored(KeyCSP),
		CORSOrigins:        linesFromCSV(e.stored(KeyCORSOrigins)),
	}
	f.CSPExtra = extraToLines(e.stored(KeyCSPExtra))
	v := View{Form: f, Errors: map[string]string{}}
	v.Defaults = describe(Defaults())
	if e.Live != nil {
		if c := e.Live.Config(); c != nil {
			v.Effective = describe(c)
		}
	}
	return v
}

func (e *Editor) storedBoolDefault(key string, def bool) bool {
	v := e.stored(key)
	if v == "" {
		return def
	}
	return strings.EqualFold(v, "true")
}

func describe(c *Config) EffectiveHeaders {
	out := EffectiveHeaders{
		XFrameOptions:     c.XFrameOptions,
		HSTS:              c.hsts(),
		ContentTypeOpts:   c.XContentTypeOptions,
		ReferrerPolicy:    c.ReferrerPolicy,
		PermissionsPolicy: c.PermissionsPolicy,
		CORSOrigins:       strings.Join(c.CORSAllowedOrigins, ", "),
	}
	if c.CSP != nil {
		out.CSP = c.CSP.String()
	}
	return out
}

func splitPermissions(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				if p := strings.TrimSpace(s[start:i]); p != "" {
					out = append(out, p)
				}
				start = i + 1
			}
		}
	}
	if p := strings.TrimSpace(s[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

func extraToLines(raw string) string {
	if raw == "" {
		return ""
	}
	m := map[string][]string{}
	if err := jsonUnmarshal(raw, &m); err != nil {
		return raw
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+" "+strings.Join(m[k], " "))
	}
	return strings.Join(lines, "\n")
}

func FormFromRequest(r *http.Request) Form {
	val := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	return Form{
		XFrameOptions:      val("xframe"),
		HSTSDisabled:       r.FormValue("hsts_disabled") == "1",
		HSTSMaxAge:         val("hsts_max_age"),
		HSTSIncludeSubs:    r.FormValue("hsts_subdomains") == "1",
		HSTSPreload:        r.FormValue("hsts_preload") == "1",
		ContentTypeOptions: val("nosniff"),
		ReferrerPolicy:     val("referrer"),
		PermissionsPolicy:  r.FormValue("permissions"),
		CSPExtra:           r.FormValue("csp_extra"),
		CSPFull:            r.FormValue("csp_full"),
		CORSOrigins:        r.FormValue("cors"),
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func validSource(directive, src string) (bool, bool) {
	if strings.ContainsAny(src, ";,\"\\<>") {
		return false, false
	}
	if cspKeyword.MatchString(src) {
		if (src == "'unsafe-inline'" || src == "'unsafe-eval'") && scriptDirectives[directive] {
			return false, true
		}
		return true, false
	}
	if cspNonce.MatchString(src) || cspScheme.MatchString(src) {
		return true, false
	}
	if src == "*" {
		return !scriptDirectives[directive], false
	}
	return cspHost.MatchString(src), false
}

func parseCSPLines(s string) (map[string][]string, string) {
	out := map[string][]string{}
	for _, line := range nonEmptyLines(s) {
		fields := strings.Fields(line)
		d := strings.ToLower(fields[0])
		if !cspDirectives[d] {
			return nil, ErrCSPExtra
		}
		for _, src := range fields[1:] {
			ok, unsafe := validSource(d, src)
			if unsafe {
				return nil, ErrCSPUnsafe
			}
			if !ok {
				return nil, ErrCSPExtra
			}
		}
		out[d] = append(out[d], fields[1:]...)
	}
	return out, ""
}

func validateFullCSP(s string) bool {
	for _, part := range strings.Split(s, ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		d := strings.ToLower(fields[0])
		if !cspDirectives[d] {
			return false
		}
		for _, src := range fields[1:] {
			if ok, _ := validSource(d, src); !ok {
				return false
			}
		}
	}
	return len(strings.Fields(strings.ReplaceAll(s, ";", " "))) > 0
}

func validOrigin(o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return false
	}
	return !strings.ContainsAny(o, "*, ;\"")
}

func (e *Editor) Validate(f Form) (map[string]string, map[string]string) {
	errs := map[string]string{}
	vals := map[string]string{}

	if f.XFrameOptions != "" && !contains(XFrameChoices, f.XFrameOptions) {
		errs["xframe"] = ErrXFrame
	}
	vals[KeyXFrameOptions] = f.XFrameOptions

	maxAge := f.HSTSMaxAge
	if maxAge != "" {
		n, err := strconv.Atoi(maxAge)
		if err != nil || n < 0 || n > 63072000 {
			errs["hsts_max_age"] = ErrHSTSMaxAge
		} else if f.HSTSPreload && !f.HSTSDisabled && (n < 31536000 || !f.HSTSIncludeSubs) {
			errs["hsts_preload"] = ErrHSTSPreload
		}
	}
	vals[KeyHSTSMaxAge] = maxAge
	vals[KeyHSTSDisabled] = boolStr(f.HSTSDisabled)
	vals[KeyHSTSIncludeSubs] = boolStr(f.HSTSIncludeSubs)
	vals[KeyHSTSPreload] = boolStr(f.HSTSPreload)

	if f.ContentTypeOptions != "" && f.ContentTypeOptions != "nosniff" {
		errs["nosniff"] = ErrNoSniff
	}
	vals[KeyXContentTypeOptions] = f.ContentTypeOptions

	if f.ReferrerPolicy != "" && !contains(ReferrerChoices, f.ReferrerPolicy) {
		errs["referrer"] = ErrReferrer
	}
	vals[KeyReferrerPolicy] = f.ReferrerPolicy

	var perms []string
	for _, line := range nonEmptyLines(f.PermissionsPolicy) {
		name, list, ok := strings.Cut(line, "=")
		name, list = strings.TrimSpace(name), strings.TrimSpace(list)
		if !ok || !featureName.MatchString(name) || !featureList.MatchString(list) {
			errs["permissions"] = ErrPermissions
			break
		}
		perms = append(perms, name+"="+list)
	}
	vals[KeyPermissionsPolicy] = strings.Join(perms, ", ")

	extra, code := parseCSPLines(f.CSPExtra)
	if code != "" {
		errs["csp_extra"] = code
	}
	extraJSON := ""
	if len(extra) > 0 {
		extraJSON = jsonMarshalSorted(extra)
	}
	vals[KeyCSPExtra] = extraJSON

	full := strings.TrimSpace(strings.ReplaceAll(f.CSPFull, "\n", " "))
	if full != "" && !validateFullCSP(full) {
		errs["csp_full"] = ErrCSPFull
	}
	vals[KeyCSP] = full

	var origins []string
	for _, o := range nonEmptyLines(strings.ReplaceAll(f.CORSOrigins, ",", "\n")) {
		if !validOrigin(o) {
			errs["cors"] = ErrCORS
			break
		}
		origins = append(origins, o)
	}
	vals[KeyCORSOrigins] = strings.Join(origins, ",")
	return vals, errs
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (e *Editor) Save(vals map[string]string) ([]Change, error) {
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var changes []Change
	var firstErr error
	for _, k := range keys {
		old := e.stored(k)
		nv := vals[k]
		if k == KeyHSTSIncludeSubs || k == KeyHSTSPreload {
			if old == "" && nv == "true" {
				continue
			}
		}
		if k == KeyHSTSDisabled && old == "" && nv == "false" {
			continue
		}
		if old == nv {
			continue
		}
		if err := e.Set(k, nv); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("saving %s: %w", k, err)
			}
			continue
		}
		changes = append(changes, Change{Key: k, Old: old, New: nv})
	}
	if len(changes) > 0 {
		if e.Reload != nil {
			e.Reload()
		} else if e.Live != nil {
			e.Live.Reload()
		}
	}
	return changes, firstErr
}

func (e *Editor) Reset() ([]Change, error) {
	vals := map[string]string{
		KeyXFrameOptions: "", KeyHSTSMaxAge: "", KeyHSTSDisabled: "", KeyHSTSIncludeSubs: "", KeyHSTSPreload: "",
		KeyCSP: "", KeyCSPExtra: "", KeyXContentTypeOptions: "", KeyReferrerPolicy: "", KeyPermissionsPolicy: "", KeyCORSOrigins: "",
	}
	return e.Save(vals)
}
