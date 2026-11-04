package logger

import (
	"log"
	"time"
)

type Options struct {
	AppName               string
	DefaultServiceName    string
	DefaultTimeZone       string
	BuildVersionUpLevels  int
	SentryServiceSetsName bool
	ConfigValue           func(key string) string
	TenantTag             func() string
	TelegramBotToken      func() string
	TelegramChatID        func() string
	TelegramBaseOverride  func() string
}

type settings struct {
	Options
}

var cfg = settings{Options: Options{AppName: "app", BuildVersionUpLevels: 1}}

func (s settings) value(key string) string {
	if s.ConfigValue == nil {
		return ""
	}
	return s.ConfigValue(key)
}

func (s settings) tenantTag() string {
	if s.TenantTag != nil {
		return s.TenantTag()
	}
	return serviceName
}

func Setup(o Options) {
	if o.AppName == "" {
		o.AppName = "app"
	}
	if o.BuildVersionUpLevels < 1 {
		o.BuildVersionUpLevels = 1
	}
	cfg = settings{Options: o}
	if o.DefaultServiceName != "" && serviceName == "unknown" {
		serviceName = o.DefaultServiceName
	}
	if o.DefaultTimeZone != "" {
		defaultLogTimeZone = o.DefaultTimeZone
		loc, err := time.LoadLocation(o.DefaultTimeZone)
		if err != nil {
			log.Printf("logger: %s tzdata unavailable (%v), log timestamps will use system local time until SetTimeZone is called", o.DefaultTimeZone, err)
			loc = time.Local
		}
		logTimeZone.Store(loc)
	}
	if o.TelegramBotToken != nil {
		TelegramBotTokenFn = o.TelegramBotToken
	}
	if o.TelegramChatID != nil {
		TelegramChatIDFn = o.TelegramChatID
	}
	if o.TelegramBaseOverride != nil {
		TelegramBaseOverrideFn = o.TelegramBaseOverride
	}
}

func SetTelegramBaseOverride(fn func() string) { TelegramBaseOverrideFn = fn }
func SetTelegramBotToken(fn func() string)     { TelegramBotTokenFn = fn }
func SetTelegramChatID(fn func() string)       { TelegramChatIDFn = fn }
func SetTelegramBaseURL(base string)           { TelegramBaseURL = base }

func CurrentTelegramBaseURL() string { return TelegramBaseURL }
