// Package emailchange is the shared "change my sign-in email" flow: a
// short-lived, single-use token is mailed to the new address and the change is
// applied only when its owner confirms it. Storage is three nullable columns
// on the users table (pending address, token hash, expiry) behind the Store
// interface, so a login magic link can never overwrite or extend a pending
// change. Re-authentication, the mail itself, notices and audit trails stay in
// each app.
package emailchange

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// TTL is how long a confirmation link stays valid.
const TTL = 5 * time.Minute

var (
	ErrInvalidEmail = errors.New("emailchange: invalid email address")
	ErrSameEmail    = errors.New("emailchange: address unchanged")
	ErrTaken        = errors.New("emailchange: address belongs to another account")
	ErrNotFound     = errors.New("emailchange: no pending change for this token")
	ErrExpired      = errors.New("emailchange: link expired")
	ErrSession      = errors.New("emailchange: link belongs to another signed-in account")
)

// Pending is a requested, not yet confirmed change. HolderID is set when
// Confirm returns ErrTaken or when AllowTaken lets a taken address through.
type Pending struct {
	UserID   int64
	NewEmail string
	Expires  time.Time
	HolderID int64
}

// Store is the persistence the flow needs. EmailHolder returns 0 when no other
// account uses the address. Apply must swap the address and clear the pending
// columns in one statement, and return ErrNotFound when nothing matched.
type Store interface {
	SetPending(ctx context.Context, userID int64, newEmail, tokenHash string, expires time.Time) error
	FindPending(ctx context.Context, tokenHash string) (Pending, error)
	EmailHolder(ctx context.Context, email string, exceptUserID int64) (int64, error)
	Apply(ctx context.Context, userID int64, newEmail string) error
	ClearPending(ctx context.Context, userID int64) error
}

// Options tune Request and Confirm. AllowTaken lets an address that another
// account already holds proceed, for apps that merge accounts on confirm
// (the caller then reads Pending.HolderID and must not call Apply itself
// through Confirm).
type Options struct {
	AllowTaken bool
}

// HashToken is how tokens are stored: the mailed value is never persisted.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Normalize trims and validates an address and returns it as typed.
func Normalize(email string) (string, error) {
	email = strings.TrimSpace(email)
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || strings.ContainsAny(email, " <>") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// Request validates the new address, stores a pending change and returns the
// token to mail. The caller must have re-authenticated the user first. On any
// error nothing is left pending.
func Request(ctx context.Context, st Store, userID int64, current, newEmail string, opt Options) (token string, p Pending, err error) {
	newEmail, err = Normalize(newEmail)
	if err != nil {
		return "", p, err
	}
	if strings.EqualFold(newEmail, strings.TrimSpace(current)) {
		return "", p, ErrSameEmail
	}
	holder, err := st.EmailHolder(ctx, newEmail, userID)
	if err != nil {
		return "", p, fmt.Errorf("emailchange: holder lookup user_id=%d: %w", userID, err)
	}
	if holder != 0 && !opt.AllowTaken {
		return "", p, ErrTaken
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", p, fmt.Errorf("emailchange: token generation: %w", err)
	}
	token = hex.EncodeToString(raw)
	expires := time.Now().Add(TTL)
	if err = st.SetPending(ctx, userID, newEmail, HashToken(token), expires); err != nil {
		return "", p, fmt.Errorf("emailchange: store pending user_id=%d: %w", userID, err)
	}
	return token, Pending{UserID: userID, NewEmail: newEmail, Expires: expires, HolderID: holder}, nil
}

// Cancel drops a pending change, used when the mail could not be sent.
func Cancel(ctx context.Context, st Store, userID int64) error {
	if err := st.ClearPending(ctx, userID); err != nil {
		return fmt.Errorf("emailchange: cancel user_id=%d: %w", userID, err)
	}
	return nil
}

// Link builds the confirmation URL from the app's base address.
func Link(base, path, token string) string {
	return strings.TrimRight(base, "/") + path + "?token=" + url.QueryEscape(token)
}

// Peek validates a token without consuming it, for the confirm page shown on
// GET. sessionUserID is the signed-in user or 0; a different signed-in user is
// refused, an anonymous visitor holding the token is allowed because the token
// was mailed to the new address and proves it.
func Peek(ctx context.Context, st Store, token string, sessionUserID int64) (Pending, error) {
	if token == "" {
		return Pending{}, ErrNotFound
	}
	p, err := st.FindPending(ctx, HashToken(token))
	if err != nil {
		return Pending{}, err
	}
	if sessionUserID != 0 && sessionUserID != p.UserID {
		return Pending{}, ErrSession
	}
	if time.Now().After(p.Expires) {
		if cerr := st.ClearPending(ctx, p.UserID); cerr != nil {
			return Pending{}, fmt.Errorf("emailchange: clear expired user_id=%d: %w (%v)", p.UserID, cerr, ErrExpired)
		}
		return Pending{}, ErrExpired
	}
	return p, nil
}

// Confirm applies the change. Uniqueness is checked again here, since another
// account may have taken the address since the request. With ErrTaken the
// pending change is cleared and Pending.HolderID names the holder.
func Confirm(ctx context.Context, st Store, token string, sessionUserID int64, opt Options) (Pending, error) {
	p, err := Peek(ctx, st, token, sessionUserID)
	if err != nil {
		return p, err
	}
	holder, err := st.EmailHolder(ctx, p.NewEmail, p.UserID)
	if err != nil {
		return p, fmt.Errorf("emailchange: holder lookup user_id=%d: %w", p.UserID, err)
	}
	if holder != 0 {
		p.HolderID = holder
		if opt.AllowTaken {
			return p, ErrTaken
		}
		if cerr := st.ClearPending(ctx, p.UserID); cerr != nil {
			return p, fmt.Errorf("emailchange: clear taken user_id=%d: %w (%v)", p.UserID, cerr, ErrTaken)
		}
		return p, ErrTaken
	}
	if err := st.Apply(ctx, p.UserID, p.NewEmail); err != nil {
		return p, err
	}
	return p, nil
}

// Start is Request plus mailing the confirmation link: it stores the pending
// change, builds the link from base and path, hands it to send and, when send
// fails, cancels the pending change so nothing is left half done. The caller
// must have proven the person's second factor first (see twofa), never a
// current password. send is how the product queues its own mail.
func Start(ctx context.Context, st Store, userID int64, current, newEmail string, opt Options, base, path string, send func(to, link string) error) (Pending, error) {
	token, p, err := Request(ctx, st, userID, current, newEmail, opt)
	if err != nil {
		return p, err
	}
	if serr := send(p.NewEmail, Link(base, path, token)); serr != nil {
		if cerr := Cancel(ctx, st, userID); cerr != nil {
			logger.Error("emailchange: %v", cerr)
		}
		return p, fmt.Errorf("emailchange: send confirmation to %q for user_id=%d: %w", p.NewEmail, userID, serr)
	}
	return p, nil
}
