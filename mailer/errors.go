package mailer

import (
	"errors"
	"net/textproto"
)

type Error struct {
	Stage     string
	Err       error
	permanent bool
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "mailer " + e.Stage
	}
	return "mailer " + e.Stage + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func IsPermanent(err error) bool {
	var me *Error
	if errors.As(err, &me) {
		return me.permanent
	}
	return false
}

func fail(stage string, err error) error {
	if err == nil {
		return nil
	}
	var me *Error
	if errors.As(err, &me) {
		return err
	}
	perm := false
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 500 {
		perm = true
	}
	return &Error{Stage: stage, Err: err, permanent: perm}
}

func invalid(stage string, err error) error {
	return &Error{Stage: stage, Err: err, permanent: true}
}
