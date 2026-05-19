package services

import "fmt"

type ErrorKind string

const (
	ErrorKindValidation ErrorKind = "validation"
	ErrorKindNotFound   ErrorKind = "not_found"
	ErrorKindConflict   ErrorKind = "conflict"
)

type AppError struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
}

func (e AppError) Error() string {
	return e.Message
}

func validationError(format string, args ...any) error {
	return AppError{Kind: ErrorKindValidation, Message: fmt.Sprintf(format, args...)}
}

func notFoundError(format string, args ...any) error {
	return AppError{Kind: ErrorKindNotFound, Message: fmt.Sprintf(format, args...)}
}

func conflictError(format string, args ...any) error {
	return AppError{Kind: ErrorKindConflict, Message: fmt.Sprintf(format, args...)}
}
