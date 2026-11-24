package twofa

import (
	"testing"

	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/CarlSinclair/csuit-libs/redis/embedded"
)

func TestPrefsDecisions(t *testing.T) {
	p := ParsePrefs(`{}`)
	en := p.Enabled(true, true)
	if len(en) != 3 || p.Satisfied(en, map[Method]bool{}) {
		t.Fatalf("enabled=%v", en)
	}
	if !p.Satisfied(en, map[Method]bool{MethodTelegram: true}) {
		t.Fatal("any one method must be enough by default")
	}
	p.All = true
	if p.Satisfied(en, map[Method]bool{MethodTelegram: true}) || !p.Satisfied(en, map[Method]bool{MethodTelegram: true, MethodTOTP: true, MethodEmail: true}) {
		t.Fatal("all means every enabled method")
	}
	if got := p.Enabled(false, false); len(got) != 1 || got[0] != MethodEmail {
		t.Fatalf("email always counts, an unlinked Telegram and a missing authenticator do not: %v", got)
	}
	off, on := false, true
	p = Prefs{Require: &off}
	if p.NeedsStep(ViaPassword, en) || p.NeedsStep(ViaPasskey, en) || p.NeedsStep(ViaMagic, en) || p.NeedsStep(ViaTelegram, en) {
		t.Fatal("with the requirement off no sign-in asks for a second factor")
	}
	p = Prefs{Require: &on}
	if !p.NeedsStep(ViaPassword, en) || !p.NeedsStep(ViaMagic, en) || !p.NeedsStep(ViaTelegram, en) {
		t.Fatal("with the requirement on every route asks for a second factor")
	}
	if p.NeedsStep(ViaMagic, []Method{MethodEmail}) || p.NeedsStep(ViaTelegram, []Method{MethodTelegram}) {
		t.Fatal("the method that signed in cannot be its own second factor")
	}
	if !p.NeedsStep(ViaPassword, []Method{MethodEmail}) {
		t.Fatal("a password sign-in with the requirement on needs the email method at least")
	}
	if (Prefs{}).NeedsStep(ViaPassword, []Method{MethodEmail}) || !(Prefs{}).NeedsStep(ViaPassword, []Method{MethodTOTP, MethodEmail}) {
		t.Fatal("an account that never chose requires 2FA only once the authenticator is set up")
	}
	if ParsePrefs("{bad").All || ParsePrefs(Prefs{All: true}.Encode()) != (Prefs{All: true}) {
		t.Fatal("prefs must round-trip and bad input fall back to defaults")
	}
}

func TestFirstFactorIsNotTheSecond(t *testing.T) {
	p := Prefs{}
	en := []Method{MethodTOTP, MethodTelegram, MethodEmail}
	done := map[Method]bool{MethodTelegram: true, Method(firstPrefix + "telegram"): true}
	if p.Satisfied(en, done) {
		t.Fatal("a Telegram sign-in must not satisfy its own second factor")
	}
	done[MethodEmail] = true
	if !p.Satisfied(en, done) {
		t.Fatal("any other method completes it")
	}
	p.All = true
	if p.Satisfied(en, done) {
		t.Fatal("all means every method besides the one that signed in")
	}
	done[MethodTOTP] = true
	if !p.Satisfied(en, done) {
		t.Fatal("all other methods done must pass")
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		t, tg, pk bool
		n         int
		lvl       string
	}{{false, false, false, 1, "red"}, {true, false, false, 2, "yellow"}, {true, true, false, 3, "yellow"}, {true, true, true, 4, "green"}}
	for _, c := range cases {
		st := Summarize(c.t, c.tg, c.pk)
		if st.Count != c.n || st.Level != c.lvl || !st.Email {
			t.Fatalf("%+v -> %+v", c, st)
		}
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
	on := true
	p := Prefs{Require: &on}
	enabled := p.EnabledSignIn(false, false, true)
	if len(enabled) != 2 {
		t.Fatalf("email and the passkey should be the sign-in methods, got %v", enabled)
	}
	if !p.NeedsStep(ViaPassword, enabled) {
		t.Fatalf("a password sign-in must ask for a second factor")
	}
	if !p.NeedsStep(ViaPasskey, enabled) {
		t.Fatalf("a passkey sign-in still needs email when the requirement is on")
	}
}
