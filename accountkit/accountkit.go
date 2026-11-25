// Package accountkit is the account "danger zone" every product shares, built
// on the TVT model: download your data, sign out of every device, optionally
// step away (deactivate), and delete the account behind a password or typed
// confirmation. The products keep the parts that differ (what is exported, what
// deleting removes or keeps) as hooks; the checks, the order of steps, the
// audit trail and the failure handling live here once.
package accountkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// Subject is the signed-in person.
type Subject struct {
	ID int64
	// Label is what a person without a password must type to confirm: their
	// email or username.
	Label       string
	HasPassword bool
}

// Error carries the i18n key a product shows for a refused or failed step.
type Error struct {
	Status int
	Key    string
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Key + ": " + e.Err.Error()
	}
	return e.Key
}
func (e *Error) Unwrap() error { return e.Err }

// Errors a Guard or Confirm hook may return are *Error values; anything else
// is treated as an internal failure ("system_error").
const (
	KeyBadPassword   = "bad_credentials"
	KeyConfirmLabel  = "confirm_label_mismatch"
	KeyConfirmFailed = "confirm_failed"
	KeySystem        = "system_error"
	KeyRevokeFailed  = "revoke_failed"
	KeyNotSignedIn   = "not_signed_in"
)

// Config wires the kit into a product. Required: Identity, CheckPassword,
// Delete, RevokeAll, ClearSession, Export.
type Config struct {
	// Identity returns the signed-in person or an *Error (usually 401).
	Identity func(r *http.Request) (Subject, error)
	// CheckPassword verifies the typed password.
	CheckPassword func(s Subject, password string) bool
	// Confirm runs an extra proof (a 2FA code) on the server; optional.
	Confirm func(r *http.Request, s Subject) error
	// Guard refuses a deletion that must not happen (the last admin); optional.
	Guard func(ctx context.Context, s Subject) error
	// Delete removes or detaches the person's data. It is the product's rule.
	Delete func(ctx context.Context, s Subject) error
	// Deactivate is the reversible step-away. Leave nil to hide it.
	Deactivate func(ctx context.Context, s Subject) error
	// RevokeAll ends every session of the person, including the current one
	// when keepCurrent is false.
	RevokeAll func(r *http.Request, s Subject, keepCurrent bool) error
	// ClearSession removes the session cookies from the response.
	ClearSession func(w http.ResponseWriter)
	// Export returns the person's data, section by section.
	Export func(ctx context.Context, s Subject) (map[string]any, error)
	// Audit records an action ("account_deleted", "account_exported",
	// "sessions_revoked", "account_deactivated"); optional.
	Audit func(r *http.Request, s Subject, action string)
	// Flash shows a message on the next page (form mode); optional when the
	// product only uses the JSON mode.
	Flash func(w http.ResponseWriter, r *http.Request, kind, key string)
	// WriteError replaces the JSON error body; optional. It receives the
	// status and the i18n key and must write the whole response.
	WriteError func(w http.ResponseWriter, r *http.Request, status int, key string)
	// WriteOK replaces the JSON success body; optional.
	WriteOK func(w http.ResponseWriter, r *http.Request, redirect string)
	// AccountURL is where form-mode failures return to ("/account").
	AccountURL string
	// HomeURL is where a deleted person lands ("/").
	HomeURL string
}

type Kit struct{ c Config }

func New(c Config) (*Kit, error) {
	if c.Identity == nil || c.CheckPassword == nil || c.Delete == nil || c.RevokeAll == nil || c.ClearSession == nil || c.Export == nil {
		return nil, errors.New("accountkit: Identity, CheckPassword, Delete, RevokeAll, ClearSession and Export are required")
	}
	if c.AccountURL == "" {
		c.AccountURL = "/account"
	}
	if c.HomeURL == "" {
		c.HomeURL = "/"
	}
	return &Kit{c: c}, nil
}

// HasDeactivate tells the panel whether to show the step-away row.
func (k *Kit) HasDeactivate() bool { return k.c.Deactivate != nil }

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "application/json") || strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (k *Kit) audit(r *http.Request, s Subject, action string) {
	if k.c.Audit != nil {
		k.c.Audit(r, s, action)
	}
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Status: http.StatusInternalServerError, Key: KeySystem, Err: err}
}

func (k *Kit) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	e := asError(err)
	if e.Status >= 500 || e.Err != nil {
		logger.Error("accountkit: %s failed: %v", what, err)
	} else {
		logger.Warn("accountkit: %s refused: %s", what, e.Key)
	}
	if wantsJSON(r) && k.c.WriteError != nil {
		k.c.WriteError(w, r, e.Status, e.Key)
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.Status)
		if encErr := json.NewEncoder(w).Encode(map[string]any{"error": e.Key}); encErr != nil {
			logger.Warn("accountkit: error response not delivered: %v", encErr)
		}
		return
	}
	if k.c.Flash != nil {
		k.c.Flash(w, r, "error", e.Key)
	}
	http.Redirect(w, r, k.c.AccountURL, http.StatusSeeOther)
}

func (k *Kit) ok(w http.ResponseWriter, r *http.Request, key, to string) {
	if wantsJSON(r) && k.c.WriteOK != nil {
		k.c.WriteOK(w, r, to)
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "redirect": to}); err != nil {
			logger.Warn("accountkit: success response not delivered: %v", err)
		}
		return
	}
	if k.c.Flash != nil && key != "" {
		k.c.Flash(w, r, "success", key)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func formValue(r *http.Request, in map[string]string, key string) string {
	if in != nil {
		return in[key]
	}
	return r.FormValue(key)
}

func readInput(r *http.Request) map[string]string {
	if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	var raw map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&raw); err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case bool:
			out[k] = strconv.FormatBool(t)
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		}
	}
	return out
}

type inputKey struct{}

func withInput(r *http.Request, in map[string]string) *http.Request {
	if in == nil {
		if r.Form == nil {
			_ = r.ParseForm()
		}
		in = map[string]string{}
		for k := range r.Form {
			in[k] = r.Form.Get(k)
		}
	}
	return r.WithContext(context.WithValue(r.Context(), inputKey{}, in))
}

// Input returns the values the person submitted (JSON scalars or form fields),
// for a product hook that needs more than the password, e.g. a choice about
// what happens to their content. It is empty outside a kit handler.
func Input(ctx context.Context) map[string]string {
	if in, ok := ctx.Value(inputKey{}).(map[string]string); ok {
		return in
	}
	return map[string]string{}
}

// verify proves the person: password if they have one, else the typed label,
// then the product's extra proof.
func (k *Kit) verify(r *http.Request, s Subject, in map[string]string) error {
	if s.HasPassword {
		if !k.c.CheckPassword(s, formValue(r, in, "password")) {
			return &Error{Status: http.StatusUnauthorized, Key: KeyBadPassword}
		}
	} else if !strings.EqualFold(strings.TrimSpace(formValue(r, in, "confirm")), strings.TrimSpace(s.Label)) {
		return &Error{Status: http.StatusBadRequest, Key: KeyConfirmLabel}
	}
	if k.c.Confirm != nil {
		if err := k.c.Confirm(r, s); err != nil {
			e := asError(err)
			if e.Key == KeySystem {
				return err
			}
			return e
		}
	}
	return nil
}

// ExportHandler streams the person's data as a JSON download.
func (k *Kit) ExportHandler(w http.ResponseWriter, r *http.Request) {
	s, err := k.c.Identity(r)
	if err != nil {
		k.fail(w, r, "export", err)
		return
	}
	data, err := k.c.Export(r.Context(), s)
	if err != nil {
		k.fail(w, r, "export of account "+idString(s), err)
		return
	}
	data["exported_at"] = time.Now().UTC().Format(time.RFC3339)
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		k.fail(w, r, "export encoding of account "+idString(s), err)
		return
	}
	k.audit(r, s, "account_exported")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="account-export.json"`)
	w.Header().Set("Cache-Control", "no-store, private")
	if _, err := w.Write(body); err != nil {
		logger.Warn("accountkit: export for account %s interrupted: %v", idString(s), err)
	}
}

// LogoutAllHandler signs the person out everywhere except this device.
func (k *Kit) LogoutAllHandler(w http.ResponseWriter, r *http.Request) {
	s, err := k.c.Identity(r)
	if err != nil {
		k.fail(w, r, "logout everywhere", err)
		return
	}
	if err := k.c.RevokeAll(r, s, true); err != nil {
		k.fail(w, r, "logout everywhere for account "+idString(s), &Error{Status: http.StatusInternalServerError, Key: KeyRevokeFailed, Err: err})
		return
	}
	k.audit(r, s, "sessions_revoked")
	k.ok(w, r, "sessions_revoked", k.c.AccountURL)
}

// DeactivateHandler is the reversible step-away.
func (k *Kit) DeactivateHandler(w http.ResponseWriter, r *http.Request) {
	if k.c.Deactivate == nil {
		http.NotFound(w, r)
		return
	}
	s, err := k.c.Identity(r)
	if err != nil {
		k.fail(w, r, "deactivate", err)
		return
	}
	in := readInput(r)
	r = withInput(r, in)
	if err := k.verify(r, s, in); err != nil {
		k.fail(w, r, "deactivate of account "+idString(s), err)
		return
	}
	if err := k.c.Deactivate(r.Context(), s); err != nil {
		k.fail(w, r, "deactivate of account "+idString(s), err)
		return
	}
	if err := k.c.RevokeAll(r, s, false); err != nil {
		logger.Error("accountkit: account %s deactivated but its sessions were not all revoked: %v", idString(s), err)
		k.fail(w, r, "deactivate sessions of account "+idString(s), &Error{Status: http.StatusInternalServerError, Key: KeyRevokeFailed, Err: err})
		return
	}
	k.audit(r, s, "account_deactivated")
	k.c.ClearSession(w)
	k.ok(w, r, "account_deactivated", k.c.HomeURL)
}

// DeleteHandler deletes the account: guard, proof, sessions, then the data.
func (k *Kit) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	s, err := k.c.Identity(r)
	if err != nil {
		k.fail(w, r, "delete", err)
		return
	}
	in := readInput(r)
	r = withInput(r, in)
	if k.c.Guard != nil {
		if err := k.c.Guard(r.Context(), s); err != nil {
			k.fail(w, r, "delete of account "+idString(s), err)
			return
		}
	}
	if err := k.verify(r, s, in); err != nil {
		k.fail(w, r, "delete of account "+idString(s), err)
		return
	}
	if err := k.c.RevokeAll(r, s, false); err != nil {
		k.fail(w, r, "delete sessions of account "+idString(s), &Error{Status: http.StatusInternalServerError, Key: KeyRevokeFailed, Err: err})
		return
	}
	if err := k.c.Delete(r.Context(), s); err != nil {
		k.fail(w, r, "delete of account "+idString(s), err)
		return
	}
	k.audit(r, s, "account_deleted")
	k.c.ClearSession(w)
	k.ok(w, r, "account_deleted", k.c.HomeURL)
}

func idString(s Subject) string {
	b, _ := json.Marshal(s.ID)
	return string(b)
}

// MessageKey is the shared locale key of a result key, for a product's Flash
// hook: Flash(kind, accountkit.MessageKey(key)).
func MessageKey(key string) string { return "accountkit.msg." + key }
