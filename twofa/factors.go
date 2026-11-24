package twofa

import (
	"encoding/json"
	"strings"

	"github.com/CarlSinclair/csuit-libs/logger"
)

type Method string

const (
	MethodTOTP     Method = "totp"
	MethodTelegram Method = "telegram"
	MethodEmail    Method = "email"
	MethodPasskey  Method = "passkey"
	MethodPassword Method = "password"
)

// Prefs is what a person chose on their 2FA page: whether 2FA is required to
// sign in (Require) and whether every enabled method is needed or any one will
// do (All). Methods are not switched on or off: the authenticator counts once
// it is set up, Telegram once a chat is linked, email always, and a passkey
// once one is registered. Require is a pointer so an account that never saved
// the page requires 2FA at sign-in whenever the authenticator is set up.
type Prefs struct {
	Require *bool `json:"require,omitempty"`
	All     bool  `json:"all,omitempty"`
}

func ParsePrefs(raw string) Prefs {
	var p Prefs
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return p
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		logger.Error("twofa: stored preferences are unreadable, using defaults: %v", err)
		return Prefs{}
	}
	return p
}

func (p Prefs) Encode() string {
	b, err := json.Marshal(p)
	if err != nil {
		logger.Error("twofa: encoding preferences failed: %v", err)
		return ""
	}
	return string(b)
}

// Enabled lists the methods that apply to this account: the authenticator when
// it is set up, Telegram when a chat is linked, and email always (every account
// has an address).
func (p Prefs) Enabled(totpOn, telegramLinked bool) []Method {
	var out []Method
	if totpOn {
		out = append(out, MethodTOTP)
	}
	if telegramLinked {
		out = append(out, MethodTelegram)
	}
	return append(out, MethodEmail)
}

// RequireLogin reports whether signing in needs a second factor. When the
// person never chose, it is on for anyone who set up the authenticator.
func (p Prefs) RequireLogin(enabled []Method) bool {
	if p.Require != nil {
		return *p.Require
	}
	for _, m := range enabled {
		if m == MethodTOTP {
			return true
		}
	}
	return false
}

// Via is how a person proved who they are before the 2FA step.
type Via string

const (
	ViaPassword Via = "password"
	ViaPasskey  Via = "passkey"
	ViaMagic    Via = "magic"
	ViaTelegram Via = "telegram"
)

// EnabledSignIn is Enabled plus the passkey, which counts as a method only at
// sign-in: someone who registered a passkey and then signs in with the
// password must also prove the passkey (or whatever else they enabled). Proofs
// for sensitive actions never include it (see ProofSatisfied).
func (p Prefs) EnabledSignIn(totpOn, telegramLinked, hasPasskey bool) []Method {
	out := p.Enabled(totpOn, telegramLinked)
	if hasPasskey {
		out = append(out, MethodPasskey)
	}
	return out
}

// WithoutPasskey drops the passkey from a method list.
func WithoutPasskey(enabled []Method) []Method {
	out := make([]Method, 0, len(enabled))
	for _, m := range enabled {
		if m != MethodPasskey {
			out = append(out, m)
		}
	}
	return out
}

const firstPrefix = "first:"

func firstMethodOf(v Via) Method {
	switch v {
	case ViaPasskey:
		return MethodPasskey
	case ViaMagic:
		return MethodEmail
	case ViaTelegram:
		return MethodTelegram
	}
	return ""
}

// NeedsStep reports whether a sign-in by this route still needs a second
// factor: only when 2FA is required for sign-in and some enabled method other
// than the one used to sign in is left. A passkey, magic link or Telegram
// sign-in replaces the password; the method it used never counts as the second
// factor. Password reset and password change are not decided here: they always
// require a proof.
func (p Prefs) NeedsStep(v Via, enabled []Method) bool {
	if !p.RequireLogin(enabled) {
		return false
	}
	first := firstMethodOf(v)
	for _, m := range enabled {
		if m != first {
			return true
		}
	}
	return false
}

// SkipsFor reports whether a sign-in needs no 2FA step at all.
func (p Prefs) SkipsFor(viaPasskey bool, enabled []Method) bool {
	v := ViaPassword
	if viaPasskey {
		v = ViaPasskey
	}
	return !p.NeedsStep(v, enabled)
}

// MarkFirst records the method a sign-in used (see Satisfied), for routes that
// replace the password.
func MarkFirst(sessionKey string, m Method) error {
	if err := MarkDone(sessionKey, m); err != nil {
		return err
	}
	return MarkDone(sessionKey, Method(firstPrefix+string(m)))
}

// Satisfied reports whether the methods completed so far are enough. A method
// recorded with MarkFirst was the sign-in itself and is left out of the count.
func (p Prefs) Satisfied(enabled []Method, done map[Method]bool) bool {
	var others []Method
	for _, m := range enabled {
		if !done[Method(firstPrefix+string(m))] {
			others = append(others, m)
		}
	}
	if len(others) == 0 {
		return true
	}
	if p.All {
		for _, m := range others {
			if !done[m] {
				return false
			}
		}
		return true
	}
	for _, m := range others {
		if done[m] {
			return true
		}
	}
	return false
}

// Status is the account's 2FA picture for the settings page: which of the four
// methods are connected, how many, and a badge colour (red for one, yellow for
// two or three, green for all four).
type Status struct {
	TOTP, Telegram, Email, Passkey bool
	Count                          int
	Level                          string
	Require, All                   bool
}

func Summarize(totpOn, telegramLinked, hasPasskey bool) Status {
	st := Status{TOTP: totpOn, Telegram: telegramLinked, Email: true, Passkey: hasPasskey}
	for _, on := range []bool{st.TOTP, st.Telegram, st.Email, st.Passkey} {
		if on {
			st.Count++
		}
	}
	switch {
	case st.Count >= 4:
		st.Level = "green"
	case st.Count >= 2:
		st.Level = "yellow"
	default:
		st.Level = "red"
	}
	return st
}

// Remaining lists the enabled methods not yet completed.
func (p Prefs) Remaining(enabled []Method, done map[Method]bool) []Method {
	var out []Method
	for _, m := range enabled {
		if !done[m] {
			out = append(out, m)
		}
	}
	return out
}

// ProofSatisfied decides a sensitive-action check for a signed-in person.
// totpOK says whether a valid authenticator or recovery code came with the
// request, and proven lists methods already checked in this request. The other methods are proven earlier, on the page, and left a proof
// that is used up here. In "all" mode nothing is used up until the code is right.
func (p Prefs) ProofSatisfied(tenant string, userID int64, enabled []Method, totpOK bool, proven ...Method) (bool, error) {
	enabled = WithoutPasskey(enabled)
	if len(enabled) == 0 {
		return true, nil
	}
	hasTOTP := false
	for _, m := range enabled {
		if m == MethodTOTP {
			hasTOTP = true
		}
	}
	done := map[Method]bool{MethodTOTP: hasTOTP && totpOK}
	for _, m := range proven {
		done[m] = true
	}
	if p.Satisfied(enabled, done) {
		return true, nil
	}
	if p.All && hasTOTP && !done[MethodTOTP] {
		return false, nil
	}
	for _, m := range enabled {
		if m == MethodTOTP || done[m] {
			continue
		}
		ok, err := TakeReauth(tenant, userID, m)
		if err != nil {
			return false, err
		}
		done[m] = ok
		if ok && !p.All {
			break
		}
	}
	return p.Satisfied(enabled, done), nil
}
