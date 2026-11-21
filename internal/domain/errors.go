// Package domain holds the entities and rules of meters-lite, free of HTTP
// and SQL. Storage and handlers translate their own errors to the ones here.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors; check them with errors.Is.
var (
	// ErrNotFound: the entity does not exist (or is not visible to the caller).
	ErrNotFound = errors.New("not found")
	// ErrConflict: a unique value (login, code) is already taken.
	ErrConflict = errors.New("already exists")
	// ErrInvalid: the input breaks a rule; the error text says which.
	ErrInvalid = errors.New("invalid input")
)

// InvalidError explains why the input was rejected. Its message is safe to
// show to the client; errors.Is(err, ErrInvalid) is true.
type InvalidError struct {
	msg string
}

func (e *InvalidError) Error() string { return e.msg }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Invalidf builds an InvalidError.
func Invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}
