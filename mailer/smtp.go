package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

const (
	defaultDialTimeout    = 5 * time.Second
	defaultSessionTimeout = 90 * time.Second
	retryKey              = "mail.smtp"
)

type Config struct {
	Host           string
	Port           string
	User           string
	Pass           string
	Hostname       string
	DialTimeout    time.Duration
	SessionTimeout time.Duration
}

func (c Config) dialTimeout() time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	return defaultDialTimeout
}

func (c Config) sessionTimeout() time.Duration {
	if c.SessionTimeout > 0 {
		return c.SessionTimeout
	}
	return defaultSessionTimeout
}

func (c Config) hostname() string {
	if strings.TrimSpace(c.Hostname) != "" {
		return strings.TrimSpace(c.Hostname)
	}
	return "localhost"
}

type Mailer struct {
	ConfigFn func() (Config, error)
}

func New(configFn func() (Config, error)) *Mailer {
	return &Mailer{ConfigFn: configFn}
}

func (m *Mailer) Send(msg Message) error {
	if m == nil || m.ConfigFn == nil {
		return invalid("config", errors.New("mailer has no configuration source"))
	}
	cfg, err := m.ConfigFn()
	if err != nil {
		return invalid("config", err)
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Port = strings.TrimSpace(cfg.Port)
	if cfg.Host == "" {
		return invalid("config", errors.New("SMTP host is not set"))
	}
	if cfg.Port == "" {
		return invalid("config", errors.New("SMTP port is not set"))
	}
	raw, from, to, err := msg.Build(time.Now())
	if err != nil {
		return err
	}
	if err := deliver(cfg, from, to, raw); err != nil {
		return err
	}
	logger.Info("Mailer: sent to=%q via=%s:%s", to, cfg.Host, cfg.Port)
	return nil
}

func (m *Mailer) SendWithRetry(p logger.RetryPolicy, msg Message) error {
	if p.Key == "" {
		p.Key = retryKey
	}
	if p.Attempts == 0 {
		p.Attempts = 3
	}
	if len(p.Backoff) == 0 {
		p.Backoff = []time.Duration{2 * time.Second, 10 * time.Second}
	}
	if p.Permanent == nil {
		p.Permanent = IsPermanent
	}
	return logger.Retry(p, "Mailer send", func() error { return m.Send(msg) })
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolve(cfg Config) ([]net.IP, error) {
	if ip := net.ParseIP(cfg.Host); ip != nil {
		return []net.IP{ip}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.dialTimeout())
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("lookup %q: %w", cfg.Host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("lookup %q: no addresses", cfg.Host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	sort.SliceStable(ips, func(i, j int) bool {
		return ips[i].To4() != nil && ips[j].To4() == nil
	})
	return ips, nil
}

func deliver(cfg Config, from, to string, raw []byte) error {
	ips, err := resolve(cfg)
	if err != nil {
		return fail("resolve", err)
	}
	dialer := net.Dialer{Timeout: cfg.dialTimeout()}
	var lastErr error
	for _, ip := range ips {
		addr := net.JoinHostPort(ip.String(), cfg.Port)
		conn, derr := dialer.Dial("tcp", addr)
		if derr != nil {
			logger.Warn("Mailer: dial failed addr=%s host=%s err=%v", addr, cfg.Host, derr)
			lastErr = fail("dial", derr)
			continue
		}
		serr := session(conn, cfg, from, to, raw)
		if serr == nil {
			return nil
		}
		if IsPermanent(serr) {
			return serr
		}
		logger.Warn("Mailer: session failed addr=%s host=%s err=%v", addr, cfg.Host, serr)
		lastErr = serr
	}
	if lastErr == nil {
		lastErr = fail("deliver", errors.New("no address to try"))
	}
	return lastErr
}

func session(conn net.Conn, cfg Config, from, to string, raw []byte) error {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(cfg.sessionTimeout())); err != nil {
		return fail("deadline", err)
	}
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	implicit := cfg.Port == "465"

	var c *smtp.Client
	if implicit {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.Handshake(); err != nil {
			return fail("tls", err)
		}
		cl, err := smtp.NewClient(tc, cfg.Host)
		if err != nil {
			return fail("greeting", err)
		}
		c = cl
	} else {
		cl, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fail("greeting", err)
		}
		c = cl
	}
	defer c.Close()

	if err := c.Hello(cfg.hostname()); err != nil {
		return fail("ehlo", err)
	}
	if !implicit {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fail("starttls", err)
			}
		} else if !isLoopbackHost(cfg.Host) {
			return fail("starttls", errors.New("server does not offer STARTTLS, refusing to continue unencrypted"))
		}
	}

	if cfg.User != "" {
		ok, mechs := c.Extension("AUTH")
		if !ok {
			return invalid("auth", errors.New("server does not advertise AUTH"))
		}
		var auth smtp.Auth
		switch {
		case strings.Contains(strings.ToUpper(mechs), "PLAIN"):
			auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
		case strings.Contains(strings.ToUpper(mechs), "LOGIN"):
			auth = &loginAuth{username: cfg.User, password: cfg.Pass, host: cfg.Host}
		default:
			return invalid("auth", fmt.Errorf("no supported AUTH mechanism in %q", mechs))
		}
		if err := c.Auth(auth); err != nil {
			return fail("auth", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fail("mail-from", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fail("rcpt-to", err)
	}
	w, err := c.Data()
	if err != nil {
		return fail("data", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fail("data-write", err)
	}
	if err := w.Close(); err != nil {
		return fail("data-close", err)
	}
	if err := c.Quit(); err != nil {
		logger.Warn("Mailer: quit after accepted message failed host=%s err=%v", cfg.Host, err)
	}
	return nil
}

type loginAuth struct {
	username string
	password string
	host     string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLoopbackHost(server.Name) {
		return "", nil, errors.New("refusing LOGIN auth over an unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name for LOGIN auth")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(strings.TrimSpace(string(fromServer)))
	switch {
	case strings.HasPrefix(prompt, "user"):
		return []byte(a.username), nil
	case strings.HasPrefix(prompt, "pass"):
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN prompt %q", prompt)
}
