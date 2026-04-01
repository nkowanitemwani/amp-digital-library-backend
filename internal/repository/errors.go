package repository

import "errors"

// These sentinel errors are returned by repository methods and checked
// by the service layer to produce the correct HTTP response.
//
// Using errors.New (not fmt.Errorf) means callers compare with errors.Is,
// not string matching — so the check is reliable even when the error
// is wrapped with additional context.

// ErrNotFound is returned when a query finds no matching row.
// The service layer maps this to HTTP 404.
var ErrNotFound = errors.New("record not found")

// ErrConflict is returned when an optimistic lock check fails —
// the row was modified by another process between the read and the write.
// The service layer maps this to HTTP 409.
var ErrConflict = errors.New("update conflict: record was modified by another process")