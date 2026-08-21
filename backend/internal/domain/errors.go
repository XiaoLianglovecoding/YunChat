package domain

import "errors"

var (
	ErrNotImplemented  = errors.New("business use case is not implemented")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("resource conflict")
)
