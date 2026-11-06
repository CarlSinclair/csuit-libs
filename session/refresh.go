package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

var (
	ErrRefreshNotFound = errors.New("session: refresh token not found")
	ErrRefreshInvalid  = errors.New("session: refresh token is not valid")
	ErrRefreshReused   = errors.New("session: superseded refresh token reused, session revoked")
)

const (
	DefaultGrace      = 60 * time.Second
	DefaultHealWindow = 24 * time.Hour
	DefaultMaxChain   = 8
)

type RefreshRecord struct {
	UserID        int64
	SID           string
	Expired       bool
	Superseded    bool
	SupersededAgo time.Duration
	SupersededBy  string
}

type RefreshStore interface {
	Find(ctx context.Context, hash string) (RefreshRecord, error)
	Rotate(ctx context.Context, old RefreshRecord, oldHash, newHash string, userID int64, sid string, newExpiry time.Time) (bool, error)
}

type Minted struct {
	Token   string
	Hash    string
	Expires time.Time
}

type Kind int

const (
	Rotated Kind = iota + 1
	Absorbed
	Healed
	HealedElsewhere
)

type Outcome struct {
	Kind     Kind
	UserID   int64
	SID      string
	NewToken string
}

type Refresher struct {
	Store      RefreshStore
	Sessions   *Manager
	Grace      time.Duration
	HealWindow time.Duration
	MaxChain   int
}

func (r *Refresher) grace() time.Duration {
	if r.Grace > 0 {
		return r.Grace
	}
	return DefaultGrace
}

func (r *Refresher) healWindow() time.Duration {
	if r.HealWindow > 0 {
		return r.HealWindow
	}
	return DefaultHealWindow
}

func (r *Refresher) maxChain() int {
	if r.MaxChain > 0 {
		return r.MaxChain
	}
	return DefaultMaxChain
}

func (r *Refresher) Refresh(ctx context.Context, presentedHash string, sameDevice func() bool, mint func(userID int64, sid string) (Minted, error)) (Outcome, error) {
	if r == nil || r.Store == nil || r.Sessions == nil {
		return Outcome{}, errors.New("session refresher is not configured")
	}
	rec, err := r.Store.Find(ctx, presentedHash)
	if err != nil {
		if errors.Is(err, ErrRefreshNotFound) {
			return Outcome{}, ErrRefreshInvalid
		}
		return Outcome{}, fmt.Errorf("lookup refresh token: %w", err)
	}
	if rec.Expired {
		return Outcome{}, ErrRefreshInvalid
	}
	if !rec.Superseded {
		m, err := mint(rec.UserID, rec.SID)
		if err != nil {
			return Outcome{}, fmt.Errorf("mint refresh token uid=%d: %w", rec.UserID, err)
		}
		won, err := r.Store.Rotate(ctx, rec, presentedHash, m.Hash, rec.UserID, rec.SID, m.Expires)
		if err != nil {
			return Outcome{}, fmt.Errorf("rotate refresh token uid=%d: %w", rec.UserID, err)
		}
		if won {
			return Outcome{Kind: Rotated, UserID: rec.UserID, SID: rec.SID, NewToken: m.Token}, nil
		}
		rec, err = r.Store.Find(ctx, presentedHash)
		if err != nil {
			if errors.Is(err, ErrRefreshNotFound) {
				return Outcome{}, ErrRefreshInvalid
			}
			return Outcome{}, fmt.Errorf("re-read refresh token uid=%d: %w", rec.UserID, err)
		}
		if !rec.Superseded {
			return Outcome{}, fmt.Errorf("refresh token uid=%d lost its rotation without being superseded", rec.UserID)
		}
	}
	if rec.SupersededAgo <= r.grace() {
		return Outcome{Kind: Absorbed, UserID: rec.UserID, SID: rec.SID}, nil
	}
	out, healed, err := r.heal(ctx, rec, presentedHash, sameDevice, mint)
	if err != nil {
		return Outcome{}, err
	}
	if healed {
		return out, nil
	}
	logger.Warn("SESSION superseded refresh token reused outside grace uid=%d sid=%s, revoking that session", rec.UserID, rec.SID)
	if err := r.Sessions.Revoke(context.WithoutCancel(ctx), rec.UserID, rec.SID); err != nil {
		logger.Error("SESSION revoke after refresh reuse uid=%d sid=%s: %v", rec.UserID, rec.SID, err)
		return Outcome{}, errors.Join(ErrRefreshReused, fmt.Errorf("revoke session after refresh reuse uid=%d sid=%s: %w", rec.UserID, rec.SID, err))
	}
	return Outcome{}, ErrRefreshReused
}

func (r *Refresher) heal(ctx context.Context, rec RefreshRecord, presentedHash string, sameDevice func() bool, mint func(userID int64, sid string) (Minted, error)) (Outcome, bool, error) {
	if sameDevice == nil || !sameDevice() || rec.SupersededAgo > r.healWindow() {
		return Outcome{}, false, nil
	}
	headHash := presentedHash
	head := rec
	for hops := 0; head.Superseded; hops++ {
		if hops >= r.maxChain() || head.SupersededBy == "" {
			return Outcome{}, false, nil
		}
		headHash = head.SupersededBy
		next, err := r.Store.Find(ctx, headHash)
		if err != nil {
			if errors.Is(err, ErrRefreshNotFound) {
				return Outcome{}, false, nil
			}
			return Outcome{}, false, fmt.Errorf("walk refresh chain uid=%d: %w", rec.UserID, err)
		}
		if next.UserID != rec.UserID || next.Expired {
			return Outcome{}, false, nil
		}
		head = next
	}
	m, err := mint(rec.UserID, head.SID)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("mint healed refresh token uid=%d: %w", rec.UserID, err)
	}
	won, err := r.Store.Rotate(ctx, head, headHash, m.Hash, rec.UserID, head.SID, m.Expires)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("rotate healed refresh token uid=%d: %w", rec.UserID, err)
	}
	if !won {
		return Outcome{Kind: HealedElsewhere, UserID: rec.UserID, SID: head.SID}, true, nil
	}
	logger.Info("SESSION recovered a refresh token that never arrived uid=%d", rec.UserID)
	return Outcome{Kind: Healed, UserID: rec.UserID, SID: head.SID, NewToken: m.Token}, true, nil
}
