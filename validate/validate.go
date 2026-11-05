// Package validate holds the input checks and password hashing shared by the
// CSuit apps: email and username format, password strength rules, and a
// bcrypt wrapper that limits how many hashes run at once.
package validate

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/CarlSinclair/csuit-libs/logger"
	"golang.org/x/crypto/bcrypt"
)

const BcryptCost = 12

var (
	emailRegex    = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,6}$`)
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	bcryptSlots   = make(chan struct{}, 2)
)

func IsValidEmail(email string) bool { return emailRegex.MatchString(email) }

// IsValidUsername allows letters, numbers, underscores and hyphens.
func IsValidUsername(username string) bool { return usernameRegex.MatchString(username) }

func acquireBcryptSlot() func() {
	start := time.Now()
	bcryptSlots <- struct{}{}
	if waited := time.Since(start); waited > 5*time.Second {
		logger.Warn("password hashing queue: a login waited %s for a free slot", waited.Round(time.Millisecond))
	}
	return func() { <-bcryptSlots }
}

// HashPassword returns a bcrypt hash. At most two hashes run at once; further
// callers queue instead of starving the machine.
func HashPassword(password string) (string, error) {
	release := acquireBcryptSlot()
	defer release()
	b, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(b), err
}

// CheckPasswordHash compares a plain password with a bcrypt hash, queued the
// same way as HashPassword.
func CheckPasswordHash(password, hash string) bool {
	release := acquireBcryptSlot()
	defer release()
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func PasswordsMatch(p1, p2 string) bool { return p1 == p2 }

// PasswordStrengthErrors returns the i18n keys of the unmet requirements.
func PasswordStrengthErrors(password string) []string {
	var errs []string
	if len(password) < 8 {
		errs = append(errs, "password_strength_min_chars")
	}
	var upper, lower, digit, special bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			upper = true
		case unicode.IsLower(c):
			lower = true
		case unicode.IsDigit(c):
			digit = true
		case IsSpecialChar(c):
			special = true
		}
	}
	if !upper {
		errs = append(errs, "password_strength_uppercase")
	}
	if !lower {
		errs = append(errs, "password_strength_lowercase")
	}
	if !digit {
		errs = append(errs, "password_strength_digit")
	}
	if !special {
		errs = append(errs, "password_strength_special_char")
	}
	return errs
}

func IsSpecialChar(r rune) bool {
	return strings.ContainsRune("!@#$%^&*()-_+=[]{}|;:'\",.<>/?`~", r)
}
