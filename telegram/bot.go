package telegram

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CarlSinclair/csuit-libs/demoegress"
	"github.com/CarlSinclair/csuit-libs/logger"
)

var (
	ErrNotConfigured = errors.New("telegram bot is not configured")
	ErrBotBlocked    = errors.New("telegram: bot was blocked by the user or the chat is gone")
	ErrBadLogin      = errors.New("telegram login data is invalid or expired")
)

var ErrNoProxyBase = errors.New("telegram proxy base is not configured")

type Bot struct {
	Token    string
	Username string
	Secret   string
	API      string
	http     *http.Client

	seenMu sync.Mutex
	seen   map[string]time.Time

	paceMu   sync.Mutex
	nextAny  time.Time
	nextChat map[string]time.Time
}

const (
	globalGap     = 500 * time.Millisecond
	perChatGap    = 1100 * time.Millisecond
	maxRetryAfter = 30 * time.Second
)

func New(token, username, webhookSecret string) *Bot {
	return &Bot{
		Token: strings.TrimSpace(token), Username: strings.TrimPrefix(strings.TrimSpace(username), "@"), Secret: webhookSecret,
		http: &http.Client{Timeout: 15 * time.Second}, seen: map[string]time.Time{}, nextChat: map[string]time.Time{},
	}
}

func (b *Bot) base() string {
	if v := strings.TrimRight(strings.TrimSpace(b.API), "/"); v != "" {
		return v
	}
	if v := logger.TelegramBase(); v != "" {
		return v
	}
	return ""
}

func (b *Bot) Configured() bool { return b != nil && b.Token != "" && b.Username != "" }

func (b *Bot) BotID() string {
	if i := strings.IndexByte(b.Token, ':'); i > 0 {
		return b.Token[:i]
	}
	return ""
}

func (b *Bot) DeepLink(startToken string) string {
	return "tg://resolve?domain=" + url.QueryEscape(b.Username) + "&start=" + url.QueryEscape(startToken)
}

type LoginData struct {
	ID        int64
	Username  string
	FirstName string
	LastName  string
	AuthDate  time.Time
}

func (l LoginData) DisplayName() string {
	return strings.TrimSpace(l.FirstName + " " + l.LastName)
}

func (b *Bot) VerifyLogin(vals url.Values) (LoginData, error) {
	var out LoginData
	if !b.Configured() {
		return out, ErrNotConfigured
	}
	got := strings.TrimSpace(vals.Get("hash"))
	if got == "" {
		return out, ErrBadLogin
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k == "hash" || k == "" || k == "action" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+vals.Get(k))
	}
	secret := sha256.Sum256([]byte(b.Token))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(strings.Join(parts, "\n")))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(got))) != 1 {
		return out, ErrBadLogin
	}
	ad, err := strconv.ParseInt(vals.Get("auth_date"), 10, 64)
	if err != nil || ad == 0 {
		return out, ErrBadLogin
	}
	out.AuthDate = time.Unix(ad, 0)
	if time.Since(out.AuthDate) > time.Hour || out.AuthDate.After(time.Now().Add(5*time.Minute)) {
		return out, ErrBadLogin
	}
	id, err := strconv.ParseInt(vals.Get("id"), 10, 64)
	if err != nil || id <= 0 {
		return out, ErrBadLogin
	}
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	now := time.Now()
	for k, t := range b.seen {
		if now.Sub(t) > 2*time.Hour {
			delete(b.seen, k)
		}
	}
	if _, dup := b.seen[want]; dup {
		return out, ErrBadLogin
	}
	b.seen[want] = now
	out.ID, out.Username, out.FirstName, out.LastName = id, vals.Get("username"), vals.Get("first_name"), vals.Get("last_name")
	return out, nil
}

func (b *Bot) VerifyWebhook(r *http.Request) bool {
	if b.Secret == "" {
		return false
	}
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(b.Secret)) == 1
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

type rateLimitedError struct{ wait time.Duration }

func (e rateLimitedError) Error() string {
	return fmt.Sprintf("telegram rate limited, retry after %s", e.wait)
}

func (b *Bot) call(ctx context.Context, method string, payload any) error {
	_, err := b.callResult(ctx, method, payload)
	return err
}

func (b *Bot) callResult(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	if b == nil || b.Token == "" {
		return nil, ErrNotConfigured
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("telegram %s marshal: %w", method, err)
	}
	base := b.base()
	if base == "" {
		return nil, ErrNoProxyBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("telegram %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, scrub(err, b.Token))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("telegram %s: bad response status %d", method, resp.StatusCode)
	}
	if !out.OK {
		if out.ErrorCode == http.StatusForbidden || (out.ErrorCode == http.StatusBadRequest && strings.Contains(out.Description, "chat not found")) {
			return nil, fmt.Errorf("%w: %s", ErrBotBlocked, out.Description)
		}
		if out.ErrorCode == http.StatusTooManyRequests {
			return nil, rateLimitedError{wait: time.Duration(out.Parameters.RetryAfter) * time.Second}
		}
		return nil, &APIError{Method: method, Code: out.ErrorCode, Description: out.Description, MigrateToChatID: out.Parameters.MigrateToChatID}
	}
	return out.Result, nil
}

func (b *Bot) pace(ctx context.Context, chat string) error {
	b.paceMu.Lock()
	now := time.Now()
	at := b.nextAny
	if c := b.nextChat[chat]; c.After(at) {
		at = c
	}
	if at.Before(now) {
		at = now
	}
	b.nextAny = at.Add(globalGap)
	b.nextChat[chat] = at.Add(perChatGap)
	if len(b.nextChat) > 5000 {
		for k, v := range b.nextChat {
			if v.Before(now) {
				delete(b.nextChat, k)
			}
		}
	}
	b.paceMu.Unlock()
	t := time.NewTimer(time.Until(at))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func scrub(err error, token string) error {
	if token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
}

func (b *Bot) SendMessage(ctx context.Context, chatID, text string) error {
	_, err := b.SendMessageID(ctx, chatID, text)
	return err
}

type Button struct {
	Text string
	Data string
}

func (b *Bot) SendMessageID(ctx context.Context, chatID, text string) (int64, error) {
	return b.SendMessageButtons(ctx, chatID, text, nil)
}

func (b *Bot) SendMessageButtons(ctx context.Context, chatID, text string, buttons []Button) (int64, error) {
	if len(text) > 4000 {
		text = text[:4000]
	}
	payload := map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true}
	if len(buttons) > 0 {
		rows := make([][]map[string]string, 0, len(buttons))
		for _, bt := range buttons {
			rows = append(rows, []map[string]string{{"text": bt.Text, "callback_data": bt.Data}})
		}
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	res, err := b.sendPaced(ctx, chatID, "sendMessage", payload)
	if err != nil {
		return 0, err
	}
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	if jerr := json.Unmarshal(res, &sent); jerr != nil {
		return 0, nil
	}
	return sent.MessageID, nil
}

func (b *Bot) sendPaced(ctx context.Context, chatID, method string, payload any) (json.RawMessage, error) {
	if err := b.pace(ctx, chatID); err != nil {
		return nil, err
	}
	res, err := b.callResult(ctx, method, payload)
	var rl rateLimitedError
	if errors.As(err, &rl) && rl.wait > 0 && rl.wait <= maxRetryAfter {
		logger.Warn("TELEGRAM rate limited chat=%s, retrying in %s", chatID, rl.wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(rl.wait):
		}
		res, err = b.callResult(ctx, method, payload)
	}
	return res, err
}

const MaxCaption = 1024

func (b *Bot) SendHTML(ctx context.Context, chatID, text string) error {
	if len(text) > 4000 {
		text = text[:4000]
	}
	_, err := b.sendPaced(ctx, chatID, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true})
	return err
}

func (b *Bot) SendPhotoHTML(ctx context.Context, chatID, photoURL, caption string) error {
	if len(caption) > MaxCaption {
		return fmt.Errorf("telegram sendPhoto: caption is %d bytes, limit %d", len(caption), MaxCaption)
	}
	_, err := b.sendPaced(ctx, chatID, "sendPhoto", map[string]any{"chat_id": chatID, "photo": photoURL, "caption": caption, "parse_mode": "HTML"})
	return err
}

func (b *Bot) AnswerCallback(ctx context.Context, callbackID string) error {
	_, err := b.callResult(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID})
	return err
}

func (b *Bot) SetWebhook(ctx context.Context, webhookURL string) error {
	return b.call(ctx, "setWebhook", map[string]any{
		"url": webhookURL, "secret_token": b.Secret, "allowed_updates": []string{"message", "callback_query"}, "drop_pending_updates": false,
	})
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Message struct {
	MessageID      int64    `json:"message_id"`
	Text           string   `json:"text"`
	Chat           Chat     `json:"chat"`
	From           *User    `json:"from"`
	ReplyToMessage *Message `json:"reply_to_message"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code"`
	IsBot        bool   `json:"is_bot"`
}

func ParseCommand(text string) (cmd, arg string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", ""
	}
	cmd = fields[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	if len(fields) > 1 {
		arg = fields[1]
	}
	return strings.ToLower(cmd), arg
}

type BotInfo struct {
	ID        int64
	Username  string
	FirstName string
}

func (b *Bot) GetMe(ctx context.Context) (BotInfo, error) {
	if b == nil || strings.TrimSpace(b.Token) == "" {
		return BotInfo{}, ErrNotConfigured
	}
	base := b.base()
	if base == "" {
		return BotInfo{}, ErrNoProxyBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/bot"+b.Token+"/getMe", nil)
	if err != nil {
		return BotInfo{}, fmt.Errorf("telegram getMe request: %w", err)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return BotInfo{}, fmt.Errorf("telegram getMe: %w", scrub(err, b.Token))
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if rerr != nil {
		return BotInfo{}, fmt.Errorf("telegram getMe: read response (status %d): %w", resp.StatusCode, scrub(rerr, b.Token))
	}
	var out struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Result      struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return BotInfo{}, fmt.Errorf("telegram getMe: bad response status %d", resp.StatusCode)
	}
	if !out.OK {
		code := out.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return BotInfo{}, &APIError{Method: "getMe", Code: code, Description: out.Description}
	}
	if out.Result.ID == 0 || strings.TrimSpace(out.Result.Username) == "" {
		return BotInfo{}, errors.New("telegram getMe: response has no id or username")
	}
	return BotInfo{ID: out.Result.ID, Username: out.Result.Username, FirstName: out.Result.FirstName}, nil
}

func GetMe(ctx context.Context, token string) (BotInfo, error) {
	return New(token, "", "").GetMe(ctx)
}

func (b *Bot) Check(ctx context.Context) (string, error) {
	info, err := b.GetMe(ctx)
	if err != nil {
		return "", err
	}
	return info.Username, nil
}

func (b *Bot) SendMarkdown(ctx context.Context, chatID, text string) error {
	_, err := b.sendPaced(ctx, chatID, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "parse_mode": "Markdown"})
	return err
}

func (b *Bot) ResolveChatID(ctx context.Context, handleOrID string) (string, error) {
	handleOrID = strings.TrimSpace(handleOrID)
	if handleOrID == "" {
		return "", errors.New("empty chat handle")
	}
	if !strings.HasPrefix(handleOrID, "@") {
		return handleOrID, nil
	}
	raw, err := b.callResult(ctx, "getChat", map[string]any{"chat_id": handleOrID})
	if err != nil {
		return "", fmt.Errorf("telegram getChat %s: %w", handleOrID, err)
	}
	var chat struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		return "", fmt.Errorf("telegram getChat %s: bad result: %w", handleOrID, err)
	}
	return strconv.FormatInt(chat.ID, 10), nil
}

func (b *Bot) SendFile(ctx context.Context, chatID, method, field, filePath, caption string) error {
	if b == nil || b.Token == "" {
		return ErrNotConfigured
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("telegram %s open %s: %w", method, filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fields := [][2]string{{"chat_id", chatID}}
	if caption != "" {
		fields = append(fields, [2]string{"caption", caption}, [2]string{"parse_mode", "Markdown"})
	}
	for _, kv := range fields {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
			return fmt.Errorf("telegram %s %s: writing field %s failed: %w", method, filePath, kv[0], err)
		}
	}
	part, err := w.CreateFormFile(field, filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("telegram %s %s: form file failed: %w", method, filePath, err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("telegram %s %s: copy failed: %w", method, filePath, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("telegram %s %s: closing multipart body failed: %w", method, filePath, err)
	}
	if err := b.pace(ctx, chatID); err != nil {
		return err
	}
	base := b.base()
	if base == "" {
		return ErrNoProxyBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+b.Token+"/"+method, &buf)
	if err != nil {
		return fmt.Errorf("telegram %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	client := b.http
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, scrub(err, b.Token))
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if rerr != nil {
		logger.Warn("telegram %s: reading response (status %d) failed chat=%s: %v", method, resp.StatusCode, chatID, rerr)
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("telegram %s: bad response status %d", method, resp.StatusCode)
	}
	if !out.OK {
		return &APIError{Method: method, Code: out.ErrorCode, Description: out.Description, MigrateToChatID: out.Parameters.MigrateToChatID}
	}
	return nil
}

type APIError struct {
	Method          string
	Code            int
	Description     string
	MigrateToChatID int64
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var rl rateLimitedError
	if errors.As(err, &rl) {
		return true
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Code >= 500
	}
	if errors.Is(err, ErrNotConfigured) || errors.Is(err, ErrNoProxyBase) || errors.Is(err, demoegress.ErrExternalDisabled) || errors.Is(err, ErrBotBlocked) || errors.Is(err, ErrBadLogin) {
		return false
	}
	return true
}

func (b *Bot) SendWithMode(ctx context.Context, chatID, text, parseMode string) error {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	_, err := b.sendPaced(ctx, chatID, "sendMessage", payload)
	return err
}

type ChatInfo struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Title     string `json:"title"`
}

func (b *Bot) GetChat(ctx context.Context, chatID string) (ChatInfo, error) {
	raw, err := b.callResult(ctx, "getChat", map[string]any{"chat_id": chatID})
	if err != nil {
		return ChatInfo{}, fmt.Errorf("telegram getChat %s: %w", chatID, err)
	}
	var info ChatInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return ChatInfo{}, fmt.Errorf("telegram getChat %s: bad result: %w", chatID, err)
	}
	return info, nil
}

func (b *Bot) SendPayload(ctx context.Context, chatID string, payload map[string]any) (int64, error) {
	payload["chat_id"] = chatID
	raw, err := b.sendPaced(ctx, chatID, "sendMessage", payload)
	if err != nil {
		return 0, err
	}
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, fmt.Errorf("telegram sendMessage: bad result: %w", err)
	}
	return msg.MessageID, nil
}

func (b *Bot) SendHTMLRaw(ctx context.Context, chatID, text string) error {
	return b.SendWithMode(ctx, chatID, text, "HTML")
}
