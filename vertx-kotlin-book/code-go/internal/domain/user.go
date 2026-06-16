// Package domain holds the core entities and errors, free of any transport
// or database type. Mirrors the Kotlin domain package.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// User is the domain entity. Repository row mappers translate to/from this.
type User struct {
	ID        int64
	Email     string
	FullName  string
	CreatedAt time.Time
}

// NewUser is the validated input DTO used by the HTTP and gRPC create paths.
type NewUser struct {
	Email    string
	FullName string
}

// MakeNewUser validates and constructs a NewUser. It enforces the same rules
// as the Kotlin `NewUser` init block.
func MakeNewUser(email, fullName string) (NewUser, error) {
	switch {
	case !strings.Contains(email, "@"):
		return NewUser{}, &ValidationError{Msg: "email must contain '@'"}
	case strings.TrimSpace(fullName) == "":
		return NewUser{}, &ValidationError{Msg: "fullName must not be blank"}
	case len(email) > 320:
		return NewUser{}, &ValidationError{Msg: "email too long"}
	case len(fullName) > 200:
		return NewUser{}, &ValidationError{Msg: "fullName too long"}
	}
	return NewUser{Email: email, FullName: fullName}, nil
}

// ---- Typed domain errors (match the Kotlin sealed UserError) -------------

// NotFoundError indicates the requested user does not exist.
type NotFoundError struct{ ID int64 }

func (e *NotFoundError) Error() string { return fmt.Sprintf("user %d not found", e.ID) }

// DuplicateEmailError indicates a unique-email conflict.
type DuplicateEmailError struct{ Email string }

func (e *DuplicateEmailError) Error() string {
	return fmt.Sprintf("email already exists: %s", e.Email)
}

// ValidationError indicates invalid input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }
