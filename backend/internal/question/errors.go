package question

import "errors"

var ErrNotImplemented = errors.New("question service not implemented")
var ErrNotFound = errors.New("question record not found")
var ErrInvalidInput = errors.New("invalid question input")
