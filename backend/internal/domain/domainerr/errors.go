// Package domainerr contains entity-agnostic domain errors shared by services
// and repositories. HTTP handlers map these errors to stable API error codes.
package domainerr

import "errors"

var (
	ErrNotImplemented = errors.New("feature not implemented")
	ErrNotFound       = errors.New("record not found")
	ErrInvalidInput   = errors.New("invalid input")
)
