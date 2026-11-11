package apikit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	SignatureHeader = "X-Webhook-Signature"
	TimestampHeader = "X-Webhook-Timestamp"
	EventIDHeader   = "X-Webhook-Id"
	EventTypeHeader = "X-Webhook-Event"
	maxURLLen       = 500
	deliveryTimeout = 10 * time.Second
	secretPrefix    = "whsec_"
)

type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      any       `json:"data"`
}

func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("apikit: random webhook secret: %w", err)
	}
	return secretPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func NewEventID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("apikit: random event id: %w", err)
	}
	return "evt_" + hex.EncodeToString(b), nil
}

func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(secret, header string, ts int64, body []byte, tolerance time.Duration, now time.Time) bool {
	if tolerance > 0 {
		d := now.Sub(time.Unix(ts, 0))
		if d < 0 {
			d = -d
		}
		if d > tolerance {
			return false
		}
	}
	return hmac.Equal([]byte(Sign(secret, ts, body)), []byte(header))
}

func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLen {
		return errors.New("invalid_url")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid_url")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return errors.New("invalid_url")
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return errors.New("invalid_url")
	}
	return nil
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

func NewSafeClient(allowHost func(host string) error) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("apikit: bad dial address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil || blockedIP(ip) {
				return fmt.Errorf("apikit: destination %s is not allowed", host)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if allowHost != nil {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("apikit: bad dial address %q: %w", addr, err)
				}
				if err := allowHost(host); err != nil {
					return nil, err
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: deliveryTimeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
		DisableKeepAlives:     false,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   deliveryTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type SendResult struct {
	StatusCode int
	OK         bool
	Error      string
	Duration   time.Duration
}

func Send(ctx context.Context, client *http.Client, target, secret, eventID, eventType string, body []byte) SendResult {
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return SendResult{Error: "request: " + err.Error(), Duration: time.Since(started)}
	}
	ts := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "webhook-delivery/1")
	req.Header.Set(EventIDHeader, eventID)
	req.Header.Set(EventTypeHeader, eventType)
	req.Header.Set(TimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(SignatureHeader, Sign(secret, ts, body))
	resp, err := client.Do(req)
	if err != nil {
		return SendResult{Error: truncate(err.Error(), 300), Duration: time.Since(started)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	res := SendResult{StatusCode: resp.StatusCode, Duration: time.Since(started)}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		res.OK = true
	} else {
		res.Error = "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
