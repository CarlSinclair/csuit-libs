package twofa

import (
	"testing"

	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/CarlSinclair/csuit-libs/redis/embedded"
)

func TestPrefsDecisions(t *testing.T) {
	p := ParsePrefs(`{"telegram":true}`)
	en := p.Enabled(true, true)
	if len(en) != 2 || p.Satisfied(en, map[Method]bool{}) {
		t.Fatalf("enabled=%v", en)
	}
	if !p.Satisfied(en, map[Method]bool{MethodTelegram: true}) {
		t.Fatal("any one method must be enough by default")
	}
	p.All = true
	if p.Satisfied(en, map[Method]bool{MethodTelegram: true}) || !p.Satisfied(en, map[Method]bool{MethodTelegram: true, MethodTOTP: true}) {
		t.Fatal("all means every enabled method")
	}
	if got := p.Enabled(true, false); len(got) != 1 {
		t.Fatalf("telegram without a chat must not count: %v", got)
	}
	if !p.SkipsFor(true, en) || p.SkipsFor(false, en) {
		t.Fatal("passkey must skip 2FA unless the person turned the requirement on, and only for passkey sign-ins")
	}
	p.PasskeyRequires = true
	if p.SkipsFor(true, en) {
		t.Fatal("passkey must not skip once the person requires 2FA on it")
	}
	if p.NeedsStep(ViaMagic, en) || p.NeedsStep(ViaTelegram, en) || !p.NeedsStep(ViaPassword, en) {
		t.Fatal("magic and Telegram sign-ins skip 2FA unless required; password always needs it")
	}
	p.MagicRequires = true
	if !p.NeedsStep(ViaMagic, []Method{MethodTOTP, MethodEmail}) {
		t.Fatal("a required magic-link sign-in still needs the methods beyond email")
	}
	if ParsePrefs("{bad").All || ParsePrefs(p.Encode()) != p {
		t.Fatal("prefs must round-trip and bad input fall back to defaults")
	}
}

func TestApprovalAnswers(t *testing.T) {
	opts, stop, err := embedded.Start("tf-test:")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	redis.Configure(opts)
	redis.Init("")
	defer redis.Close()

	seed := func(token, secret string, chat int64) {
		redis.Client().HSet(redis.Ctx, approvalKeyPrefix+token, map[string]any{"user": 9, "chat": chat, "secret": secretHash(secret), "status": "pending"})
	}
	tok := "aaaaaaaaaaaaaaaaaaaa"
	seed(tok, "s3", 77)
	if st, _ := PollApproval(tok, "s3"); st != "pending" {
		t.Fatalf("fresh = %s", st)
	}
	if st, _ := PollApproval(tok, "nope"); st != "expired" {
		t.Fatalf("wrong secret = %s", st)
	}
	if h, _, acc := HandleCallback(CallbackPrefix+"t1:"+tok+":y", 1); !h || acc {
		t.Fatal("another chat must not be able to answer")
	}
	if h, ap, acc := HandleCallback(CallbackPrefix+"t1:"+tok+":y", 77); !h || !ap || !acc {
		t.Fatal("the right chat approves")
	}
	if _, _, acc := HandleCallback(CallbackPrefix+"t1:"+tok+":n", 77); acc {
		t.Fatal("an answered approval cannot be flipped")
	}
	if st, uid := PollApproval(tok, "s3"); st != "approved" || uid != 9 {
		t.Fatalf("approved = %s %d", st, uid)
	}
	if st, _ := PollApproval(tok, "s3"); st != "expired" {
		t.Fatalf("an approval is single use, got %s", st)
	}
	if ten, ok := CallbackTenant(CallbackPrefix + "t1:" + tok + ":y"); !ok || ten != "t1" {
		t.Fatal("tenant must be readable from the button data")
	}
	if h, _, _ := HandleCallback("other:1", 1); h {
		t.Fatal("foreign callbacks are not ours")
	}
	seed(tok, "s3", 77)
	HandleCallback(CallbackPrefix+"t1:"+tok+":n", 77)
	if st, _ := PollApproval(tok, "s3"); st != "denied" {
		t.Fatalf("denied = %s", st)
	}

	if err := MarkDone("sess", MethodTOTP); err != nil {
		t.Fatal(err)
	}
	if d, err := Done("sess"); err != nil || !d[MethodTOTP] || d[MethodTelegram] {
		t.Fatalf("done = %v err=%v", d, err)
	}
	ClearDone("sess")
	if d, _ := Done("sess"); len(d) != 0 {
		t.Fatalf("cleared = %v", d)
	}
}

func TestPasskeyCountsAtSignInOnly(t *testing.T) {
	p := Prefs{}
	enabled := p.EnabledSignIn(false, false, true)
	if len(enabled) != 1 || enabled[0] != MethodPasskey {
		t.Fatalf("a passkey alone should be the only sign-in method, got %v", enabled)
	}
	if !p.NeedsStep(ViaPassword, enabled) {
		t.Fatalf("a password sign-in must ask for the passkey")
	}
	if p.NeedsStep(ViaPasskey, enabled) {
		t.Fatalf("a passkey sign-in must not ask again")
	}
	q := Prefs{PasskeyRequires: true}
	both := q.EnabledSignIn(true, false, true)
	if q.NeedsStep(ViaPasskey, both) {
		t.Fatalf("in any-mode a passkey sign-in already satisfies the step")
	}
	ok, err := p.ProofSatisfied("t", 1, enabled, false)
	if err != nil || !ok {
		t.Fatalf("a passkey alone must not block sensitive actions: %v %v", ok, err)
	}
}
