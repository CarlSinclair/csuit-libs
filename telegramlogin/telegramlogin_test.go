package telegramlogin

import (
	"strings"
	"testing"

	"github.com/CarlSinclair/csuit-libs/redis"
	"github.com/CarlSinclair/csuit-libs/redis/embedded"
)

func TestSignInFlowWithResolveOnEmbeddedRedis(t *testing.T) {
	opts, stop, err := embedded.Start("tl-test:")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	redis.Configure(opts)
	redis.Init("")
	defer redis.Close()

	var seen Identity
	Configure(Config{
		OwnDirName: func() string { return "my-tvt" },
		Resolve: func(chatID int64, who Identity) ([]int64, error) {
			seen = who
			return []int64{42}, nil
		},
		Text: func(_, key string) string { return key },
	})

	secret, err := NewTelegramLoginSecret()
	if err != nil {
		t.Fatal(err)
	}
	token, err := CreateTelegramLoginToken(secret, "Brazil")
	if err != nil || !strings.HasPrefix(token, "login_my-tvt_") {
		t.Fatalf("token = %q err=%v", token, err)
	}
	if st, _ := PollTelegramLogin(token, secret); st != "pending" {
		t.Fatalf("fresh token = %s", st)
	}
	owned, reply, confirm, _ := ProcessTelegramLoginStartAs(token, 7, "private", "pt", Identity{Username: "tess", FirstName: "Tess"})
	if !owned || confirm != token || !strings.Contains(reply, "Brazil") {
		t.Fatalf("start owned=%v confirm=%q reply=%q", owned, confirm, reply)
	}
	if seen.Username != "tess" || seen.FirstName != "Tess" || seen.Lang != "pt" {
		t.Fatalf("resolver saw %+v", seen)
	}
	if st, _ := PollTelegramLogin(token, "wrong"); st != "expired" {
		t.Fatalf("wrong secret = %s", st)
	}
	if _, r := ProcessTelegramLoginConfirm(token, 8, "pt"); !strings.Contains(r, "tg_link_invalid") {
		t.Fatalf("a different chat must not confirm: %q", r)
	}
	if _, r := ProcessTelegramLoginConfirm(token, 7, "pt"); !strings.Contains(r, "tg_login_done") {
		t.Fatalf("confirm reply = %q", r)
	}
	if st, id := PollTelegramLogin(token, secret); st != "approved" || id != 42 {
		t.Fatalf("poll = %s %d", st, id)
	}
	if st, _ := PollTelegramLogin(token, secret); st != "expired" {
		t.Fatalf("a token is single use, got %s", st)
	}
}

func TestFlowsDoNotAcceptEachOthersTokens(t *testing.T) {
	opts, stop, err := embedded.Start("tl-kind:")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	redis.Configure(opts)
	redis.Init("")
	defer redis.Close()

	base := Config{
		OwnDirName: func() string { return "my-shop" },
		Resolve:    func(int64, Identity) ([]int64, error) { return []int64{5}, nil },
		Text:       func(_, key string) string { return key },
	}
	staff := &Flow{Config: base, Kind: "staff"}
	customer := &Flow{Config: base, Kind: "customer"}

	secret, _ := NewTelegramLoginSecret()
	token, err := customer.CreateTelegramLoginToken(secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if owned, _, confirm, _ := staff.ProcessTelegramLoginStartAs(token, 9, "private", "en", Identity{}); owned || confirm != "" {
		t.Fatal("the staff flow must not take a customer token")
	}
	if st, _ := staff.PollTelegramLogin(token, secret); st != "expired" {
		t.Fatalf("the staff flow polled a customer token: %s", st)
	}
	if owned, _, confirm, _ := customer.ProcessTelegramLoginStartAs(token, 9, "private", "en", Identity{}); !owned || confirm != token {
		t.Fatal("the customer flow must take its own token")
	}
	if owned, _ := staff.ProcessTelegramLoginConfirm(token, 9, "en"); owned {
		t.Fatal("the staff flow must not confirm a customer token")
	}
	if _, r := customer.ProcessTelegramLoginConfirm(token, 9, "en"); !strings.Contains(r, "tg_login_done") {
		t.Fatalf("confirm = %q", r)
	}
	if st, id := customer.PollTelegramLogin(token, secret); st != "approved" || id != 5 {
		t.Fatalf("poll = %s %d", st, id)
	}
}
