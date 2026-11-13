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
)

// Prefs is what a person chose on their 2FA page besides which methods are
// switched on: whether every enabled method is required or any one will do,
// whether a passkey sign-in skips 2FA, and whether Telegram approval is on.
// It is stored as one small JSON string next to the account.
type Prefs struct {
	Telegram      bool `json:"telegram,omitempty"`
	Email         bool `json:"email,omitempty"`
	All           bool `json:"all,omitempty"`
	PasskeySkips  bool `json:"passkey_skips,omitempty"`
	MagicSkips    bool `json:"magic_skips,omitempty"`
	TelegramSkips bool `json:"telegram_skips,omitempty"`
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
// it is set up, Telegram when the person turned it on and has a chat linked, and
// email when the person turned it on (every account has an address).
func (p Prefs) Enabled(totpOn, telegramLinked bool) []Method {
	var out []Method
	if totpOn {
		out = append(out, MethodTOTP)
	}
	if p.Telegram && telegramLinked {
		out = append(out, MethodTelegram)
	}
	if p.Email {
		out = append(out, MethodEmail)
	}
	return out
}

// Via is how a person proved who they are before the 2FA step.
type Via string

const (
	ViaPassword Via = "password"
	ViaPasskey  Via = "passkey"
	ViaMagic    Via = "magic"
	ViaTelegram Via = "telegram"
)

// NeedsStep reports whether a sign-in by this route still needs a 2FA step.
// A magic link proves the email method and a Telegram sign-in proves the
// Telegram method, so each counts as that method being done; the person's own
// "skip 2FA for this route" choice is honoured for passkey, magic link and
// Telegram, never for a password or a password reset.
func (p Prefs) NeedsStep(v Via, enabled []Method) bool {
	if len(enabled) == 0 {
		return false
	}
	done := map[Method]bool{}
	switch v {
	case ViaPasskey:
		if p.PasskeySkips {
			return false
		}
	case ViaMagic:
		if p.MagicSkips {
			return false
		}
		done[MethodEmail] = true
	case ViaTelegram:
		if p.TelegramSkips {
			return false
		}
		done[MethodTelegram] = true
	}
	return !p.Satisfied(enabled, done)
}

// SkipsFor reports whether a sign-in needs no 2FA step at all.
func (p Prefs) SkipsFor(viaPasskey bool, enabled []Method) bool {
	return len(enabled) == 0 || (viaPasskey && p.PasskeySkips)
}

// Satisfied reports whether the methods completed so far are enough.
func (p Prefs) Satisfied(enabled []Method, done map[Method]bool) bool {
	if len(enabled) == 0 {
		return true
	}
	if p.All {
		for _, m := range enabled {
			if !done[m] {
				return false
			}
		}
		return true
	}
	for _, m := range enabled {
		if done[m] {
			return true
		}
	}
	return false
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
